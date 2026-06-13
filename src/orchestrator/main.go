package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/chatctx"
	"eve-beemo/src/orchestrator/config"
	orchestrdb "eve-beemo/src/orchestrator/db"
	"eve-beemo/src/orchestrator/llm"
	"eve-beemo/src/orchestrator/prompts"
	"eve-beemo/src/orchestrator/routing"
	orchtools "eve-beemo/src/orchestrator/tools"
	"google.golang.org/grpc"
)

type orchestratorServer struct {
	pb.UnimplementedOrchestratorServer
	cfg                 config.Config
	tools               orchtools.Executor
	routeSelector       routeSelector
	readGrammar         func(path string) (string, error)
	callCompletion      func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error)
	callFinalMessage    func(httpURL, model, prompt string, timeout time.Duration) (string, error)
	historyMu           sync.Mutex
	pendingMu           sync.Mutex
	pendingBySession    map[string]pendingToolState
	transcriptMu        sync.Mutex
	transcriptBySession map[string][]*pb.ChatMessage
}

type toolCall struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

type pendingToolState struct {
	OriginalUserQuery string          `json:"original_user_query"`
	Tool              string          `json:"tool"`
	Args              json.RawMessage `json:"args"`
	Missing           []string        `json:"missing"`
	Question          string          `json:"question"`
}

type routeSelector interface {
	Retrieve(query string, timeout time.Duration) ([]routing.Candidate, error)
}

type routeCatalog interface {
	Routes() []routing.Route
}

type weatherConfigProvider interface {
	WeatherConfig() orchtools.WeatherConfig
}

const (
	contextSelectionMessages  = 16
	activeContextTurns        = 4
	sessionTranscriptMessages = 18
)

func (s *orchestratorServer) Chat(ctx context.Context, req *pb.ChatRequest) (*pb.ChatResponse, error) {
	start := time.Now()
	effectiveMessages := s.resolveMessages(req.GetSessionId(), req.GetMessages())
	userQuery := latestUserQuery(req.GetMessages())
	if userQuery == "" {
		userQuery = latestUserQuery(effectiveMessages)
	}
	fmt.Printf("orch.chat start session=%s user_query=%q\n", req.GetSessionId(), userQuery)
	if userQuery == "" {
		s.appendHistory(&historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			Status:    "empty_query",
		})
		return &pb.ChatResponse{Text: ""}, nil
	}

	if s.cfg.LLMHTTPURL == "" {
		s.appendHistory(&historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			UserQuery: userQuery,
			Status:    "error",
			Error:     "missing_llm_url",
		})
		return nil, fmt.Errorf("LLM_HTTP_URL missing")
	}

	readGrammar := s.readGrammar
	if readGrammar == nil {
		readGrammar = readGrammarFile
	}
	grammar, err := readGrammar(s.cfg.DecisionGrammarPath)
	if err != nil {
		s.appendHistory(&historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			UserQuery: userQuery,
			Status:    "error",
			Error:     fmt.Sprintf("grammar_read: %v", err),
		})
		return nil, err
	}

	callCompletion := s.callCompletion
	if callCompletion == nil {
		callCompletion = llm.CallChatWithGrammar
	}
	callFinalMessage := s.callFinalMessage
	if callFinalMessage == nil {
		callFinalMessage = llm.CallOnce
	}

	callTimeout := time.Duration(s.cfg.LLMTimeoutMs) * time.Millisecond
	embeddingTimeout := time.Duration(s.cfg.EmbeddingTimeoutMs) * time.Millisecond
	activeContext := chatctx.Build(effectiveMessages, contextSelectionMessages, activeContextTurns)
	routingQuery := strings.TrimSpace(activeContext.UserEvidence)
	if routingQuery == "" {
		routingQuery = userQuery
	}

	text := ""
	fromPending := false
	pending, hasPending := s.getPending(req.GetSessionId())
	originQuery := userQuery
	if hasPending && strings.TrimSpace(pending.OriginalUserQuery) != "" {
		originQuery = pending.OriginalUserQuery
	}

	if hasPending {
		if filledCall, ok, ferr := orchtools.TryFillPending(orchtools.PendingFillRequest{
			Action:  pending.Tool,
			Args:    pending.Args,
			Missing: pending.Missing,
			Reply:   userQuery,
		}); ferr != nil {
			return nil, ferr
		} else if ok {
			filledText, jerr := json.Marshal([]toolCall{fromPlannedCall(filledCall)})
			if jerr != nil {
				return nil, jerr
			}
			text = string(filledText)
			fromPending = true
		}
	}

	if hasPending && strings.TrimSpace(text) == "" {
		if inferred, ok, ierr := orchtools.InferToolCall(userQuery); ierr != nil {
			return nil, ierr
		} else if ok && pendingInterruptedByNewCall(pending, inferred) {
			s.clearPending(req.GetSessionId())
			hasPending = false
			originQuery = userQuery
			inferredText, jerr := json.Marshal([]toolCall{fromPlannedCall(inferred)})
			if jerr != nil {
				return nil, jerr
			}
			text = string(inferredText)
		}
	}

	if hasPending && strings.TrimSpace(text) == "" {
		resumePrompt := prompts.ResumeToolUpdate(
			pending.OriginalUserQuery,
			activeContext.Transcript,
			pending.Tool,
			string(pending.Args),
			pending.Missing,
			pending.Question,
			userQuery,
		)
		resumeText, rerr := callCompletion(s.cfg.LLMHTTPURL, s.cfg.LLMModel, resumePrompt, grammar, callTimeout)
		if rerr != nil {
			return nil, rerr
		}
		resumeCalls, perr := parseToolCalls(resumeText)
		if perr != nil {
			return nil, perr
		}
		resumeCalls = filterSupportedToolCalls(resumeCalls)
		if len(resumeCalls) == 1 {
			mergedCall, ok, merr := mergePendingToolCall(pending, resumeCalls[0])
			if merr != nil {
				return nil, merr
			}
			if ok && pendingFieldsSatisfied(pending.Missing, mergedCall.Args) {
				mergedText, jerr := json.Marshal([]toolCall{mergedCall})
				if jerr != nil {
					return nil, jerr
				}
				text = string(mergedText)
				fromPending = true
			}
		}
		if strings.TrimSpace(text) == "" {
			s.clearPending(req.GetSessionId())
			hasPending = false
			originQuery = userQuery
		}
	}

	var routeCandidates []routing.Candidate
	var catalogRoutes []routing.Route
	if selector, ok := s.routeSelector.(routeCatalog); ok {
		catalogRoutes = selector.Routes()
	}

	if strings.TrimSpace(text) == "" {
		if inferred, ok, ierr := orchtools.InferToolCall(userQuery); ierr != nil {
			return nil, ierr
		} else if ok && supportedTool(inferred.Action) {
			inferredText, jerr := json.Marshal([]toolCall{fromPlannedCall(inferred)})
			if jerr != nil {
				return nil, jerr
			}
			text = string(inferredText)
		}
	}

	if strings.TrimSpace(text) == "" {
		prompt := prompts.ToolDecision(userQuery, activeContext.Transcript)
		if s.routeSelector != nil {
			candidates, rerr := s.routeSelector.Retrieve(routingQuery, embeddingTimeout)
			if rerr != nil {
				fmt.Printf("orch.chat route_retrieval session=%s status=fallback err=%v\n", req.GetSessionId(), rerr)
			} else if len(candidates) > 0 {
				routeCandidates = candidates
				prompt = prompts.RoutedToolDecision(userQuery, activeContext.Transcript, routing.FormatCandidates(candidates))
			}
		}
		text, err = callCompletion(s.cfg.LLMHTTPURL, s.cfg.LLMModel, prompt, grammar, callTimeout)
		if err != nil {
			s.appendHistory(&historyEntry{
				Timestamp: time.Now().Format(time.RFC3339),
				SessionID: req.GetSessionId(),
				UserQuery: userQuery,
				Status:    "error",
				Error:     fmt.Sprintf("llm_decision: %v", err),
			})
			return nil, err
		}
	}

	toolCalls, err := parseToolCalls(text)
	if err != nil {
		s.appendHistory(&historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			UserQuery: userQuery,
			Decision:  text,
			Status:    "error",
			Error:     fmt.Sprintf("tool_parse: %v", err),
		})
		return nil, err
	}

	if len(toolCalls) == 0 {
		retryPrompt := prompts.RetryToolDecision(userQuery, activeContext.Transcript)
		if retryText, rerr := callCompletion(s.cfg.LLMHTTPURL, s.cfg.LLMModel, retryPrompt, grammar, callTimeout); rerr == nil {
			if retryCalls, perr := parseToolCalls(retryText); perr == nil && len(retryCalls) > 0 {
				toolCalls = retryCalls
				text = retryText
			}
		}
	}

	toolCalls = filterSupportedToolCalls(toolCalls)
	if len(toolCalls) == 0 {
		text = "[]"
	}
	toolsRequested := toolNames(toolCalls)
	fmt.Printf("orch.chat decision_raw session=%s text=%s tools=%v\n", req.GetSessionId(), text, toolsRequested)

	toolResult := ""
	successfulResults := []orchtools.Result{}
	evidenceText := activeContext.UserEvidence
	if strings.TrimSpace(evidenceText) == "" {
		evidenceText = originQuery
		if hasPending && strings.TrimSpace(userQuery) != "" && userQuery != originQuery {
			evidenceText = originQuery + "\n" + userQuery
		}
	}

	for _, tool := range toolCalls {
		explicitTool := tool
		if !fromPending {
			groundedTool, gerr := orchtools.GroundCall(evidenceText, toPlannedCall(tool))
			if gerr != nil {
				return nil, gerr
			}
			explicitTool = fromPlannedCall(groundedTool)
		}
		if _, _, merr := routing.MatchCall(routeCandidates, catalogRoutes, explicitTool.Tool, explicitTool.Args); merr != nil {
			return nil, merr
		}

		resolvedTool := toPlannedCall(explicitTool)
		switch explicitTool.Tool {
		case "calculator":
			resolved, rerr := orchtools.ResolveCalculatorCall(resolvedTool, userQuery)
			if rerr != nil {
				return nil, rerr
			}
			resolvedTool = resolved
		case "weather":
			resolved, rerr := orchtools.ResolveWeatherCall(resolvedTool, userQuery)
			if rerr != nil {
				return nil, rerr
			}
			resolvedTool = resolved
			resolved, rerr = resolveWeatherLocation(ctx, s.tools, s.cfg, resolvedTool)
			if rerr != nil {
				return nil, rerr
			}
			resolvedTool = resolved
		case "older_sister":
			resolved, rerr := orchtools.ResolveOlderSisterCall(resolvedTool, userQuery)
			if rerr != nil {
				return nil, rerr
			}
			resolvedTool = resolved
		}

		tool = fromPlannedCall(resolvedTool)
		fmt.Printf("orch.chat tool_call session=%s tool=%s args=%s\n", req.GetSessionId(), tool.Tool, string(tool.Args))
		result, rerr := s.tools.Execute(ctx, orchtools.Request{
			SessionID: req.GetSessionId(),
			Action:    tool.Tool,
			Args:      tool.Args,
		})
		if rerr != nil {
			s.appendHistory(&historyEntry{
				Timestamp: time.Now().Format(time.RFC3339),
				SessionID: req.GetSessionId(),
				UserQuery: userQuery,
				Decision:  text,
				Tools:     toolsRequested,
				Status:    "error",
				Error:     fmt.Sprintf("tool_call: %v", rerr),
			})
			return nil, rerr
		}
		if result.Status == "needs_input" {
			s.setPending(req.GetSessionId(), pendingToolState{
				OriginalUserQuery: originQuery,
				Tool:              tool.Tool,
				Args:              cloneRawMessage(tool.Args),
				Missing:           append([]string(nil), result.Missing...),
				Question:          result.Question,
			})
			s.setTranscript(req.GetSessionId(), appendAssistantMessage(effectiveMessages, result.Question))
			s.appendHistory(&historyEntry{
				Timestamp:  time.Now().Format(time.RFC3339),
				SessionID:  req.GetSessionId(),
				UserQuery:  userQuery,
				Decision:   text,
				Tools:      toolsRequested,
				ToolResult: fmt.Sprintf("status=%s missing=%v question=%s", result.Status, result.Missing, result.Question),
				Response:   result.Question,
				Status:     "needs_input",
			})
			return &pb.ChatResponse{Text: result.Question}, nil
		}
		s.clearPending(req.GetSessionId())
		successfulResults = append(successfulResults, result)
		toolResult += fmt.Sprintf("tool=%s result=%s\n", result.Action, result.Output)
	}

	if directText, ok := directToolResponse(successfulResults, userQuery); ok {
		s.appendHistory(&historyEntry{
			Timestamp:  time.Now().Format(time.RFC3339),
			SessionID:  req.GetSessionId(),
			UserQuery:  userQuery,
			Decision:   text,
			Tools:      toolsRequested,
			ToolResult: strings.TrimSpace(toolResult),
			Response:   directText,
			Status:     "ok",
		})
		s.setTranscript(req.GetSessionId(), appendAssistantMessage(effectiveMessages, directText))
		fmt.Printf("orch.chat done session=%s status=ok path=direct ms=%d\n", req.GetSessionId(), time.Since(start).Milliseconds())
		return &pb.ChatResponse{Text: directText}, nil
	}

	followup := prompts.FinalResponse(originQuery, userQuery, activeContext.Transcript, text, toolResult)
	finalText, err := callFinalMessage(s.cfg.LLMHTTPURL, s.cfg.LLMModel, followup, callTimeout)
	if err != nil {
		s.appendHistory(&historyEntry{
			Timestamp:  time.Now().Format(time.RFC3339),
			SessionID:  req.GetSessionId(),
			UserQuery:  userQuery,
			Decision:   text,
			Tools:      toolsRequested,
			ToolResult: strings.TrimSpace(toolResult),
			Status:     "error",
			Error:      fmt.Sprintf("llm_followup: %v", err),
		})
		return nil, err
	}
	s.appendHistory(&historyEntry{
		Timestamp:  time.Now().Format(time.RFC3339),
		SessionID:  req.GetSessionId(),
		UserQuery:  userQuery,
		Decision:   text,
		Tools:      toolsRequested,
		ToolResult: strings.TrimSpace(toolResult),
		Response:   finalText,
		Status:     "ok",
	})
	s.setTranscript(req.GetSessionId(), appendAssistantMessage(effectiveMessages, finalText))
	fmt.Printf("orch.chat done session=%s status=ok path=final ms=%d\n", req.GetSessionId(), time.Since(start).Milliseconds())
	return &pb.ChatResponse{Text: finalText}, nil
}

func resolveWeatherLocation(ctx context.Context, tools orchtools.Executor, cfg config.Config, call orchtools.PlannedCall) (orchtools.PlannedCall, error) {
	argsMap := map[string]json.RawMessage{}
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &argsMap); err != nil {
			return orchtools.PlannedCall{}, err
		}
	}
	locationQuery := strings.TrimSpace(orchtools.StringFieldRaw(argsMap["location"]))
	if locationQuery == "" {
		return call, nil
	}
	weatherCfg := orchtools.WeatherConfig{GeocodingURL: cfg.WeatherGeocodingURL}
	if provider, ok := tools.(weatherConfigProvider); ok {
		weatherCfg = provider.WeatherConfig()
		if strings.TrimSpace(weatherCfg.GeocodingURL) == "" {
			weatherCfg.GeocodingURL = cfg.WeatherGeocodingURL
		}
	}
	location, err := orchtools.GeocodeWeatherLocation(ctx, weatherCfg, locationQuery)
	if err != nil {
		return orchtools.PlannedCall{}, err
	}
	args := map[string]any{}
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return orchtools.PlannedCall{}, err
		}
	}
	if strings.TrimSpace(location.Query) != "" {
		args["location"] = strings.TrimSpace(location.Query)
	}
	args["location_name"] = strings.TrimSpace(location.Name)
	args["latitude"] = strings.TrimSpace(location.Latitude)
	args["longitude"] = strings.TrimSpace(location.Longitude)
	if strings.TrimSpace(location.Timezone) != "" {
		args["timezone"] = strings.TrimSpace(location.Timezone)
	}
	updated, err := json.Marshal(args)
	if err != nil {
		return orchtools.PlannedCall{}, err
	}
	call.Args = updated
	return call, nil
}

func directToolResponse(results []orchtools.Result, userQuery string) (string, bool) {
	if len(results) != 1 {
		return "", false
	}
	result := results[0]
	output := strings.TrimSpace(result.Output)
	if output == "" || strings.TrimSpace(result.Action) != "get_time" || !isSimpleCurrentTimeQuery(userQuery) {
		return "", false
	}
	timestamp, err := time.Parse(time.RFC3339, output)
	if err != nil {
		return output, true
	}
	return fmt.Sprintf("It is %s.", timestamp.Format("3:04 PM on January 2, 2006")), true
}

func pendingInterruptedByNewCall(pending pendingToolState, inferred orchtools.PlannedCall) bool {
	pendingTool := strings.TrimSpace(pending.Tool)
	inferredAction := strings.TrimSpace(inferred.Action)
	return pendingTool != "" && inferredAction != "" && pendingTool != inferredAction
}

func pendingFieldsSatisfied(missing []string, args json.RawMessage) bool {
	if len(missing) == 0 {
		return true
	}
	values := map[string]json.RawMessage{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &values); err != nil {
			return false
		}
	}
	for _, field := range missing {
		raw, ok := values[strings.TrimSpace(field)]
		if !ok || !rawValuePresent(raw) {
			return false
		}
	}
	return true
}

func rawValuePresent(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "[]" && trimmed != `""`
}

func isSimpleCurrentTimeQuery(text string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	if lower == "" {
		return false
	}
	for _, term := range []string{
		"tomorrow", "yesterday", "next ", "last ", "after", "before", "from today",
		"in ", " ago", "later", "will ", "days", "weeks", "months", "years",
	} {
		if strings.Contains(lower, term) {
			return false
		}
	}
	return strings.Contains(lower, "what time") ||
		strings.Contains(lower, "what is the time") ||
		strings.Contains(lower, "what's the time") ||
		strings.Contains(lower, "whats the time") ||
		strings.Contains(lower, "tell me the time") ||
		strings.Contains(lower, "current time") ||
		strings.Contains(lower, "time is it") ||
		strings.Contains(lower, "right now") ||
		strings.Contains(lower, "today's date") ||
		strings.Contains(lower, "todays date") ||
		strings.Contains(lower, "current date") ||
		strings.Contains(lower, "what date") ||
		strings.Contains(lower, "what day") ||
		strings.Contains(lower, "what month") ||
		strings.Contains(lower, "what year") ||
		lower == "time" ||
		lower == "date"
}

func latestUserQuery(messages []*pb.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(messages[i].GetRole())) != "user" {
			continue
		}
		content := strings.TrimSpace(messages[i].GetContent())
		if content != "" {
			return content
		}
	}
	return ""
}

func cloneMessages(messages []*pb.ChatMessage) []*pb.ChatMessage {
	cloned := make([]*pb.ChatMessage, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		cloned = append(cloned, &pb.ChatMessage{
			Role:    message.GetRole(),
			Content: message.GetContent(),
		})
	}
	return cloned
}

func normalizeMessages(messages []*pb.ChatMessage) []*pb.ChatMessage {
	normalized := make([]*pb.ChatMessage, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(message.GetRole()))
		content := strings.TrimSpace(message.GetContent())
		if role == "" || content == "" {
			continue
		}
		normalized = append(normalized, &pb.ChatMessage{
			Role:    role,
			Content: content,
		})
	}
	return normalized
}

func trimMessages(messages []*pb.ChatMessage) []*pb.ChatMessage {
	normalized := normalizeMessages(messages)
	if len(normalized) <= sessionTranscriptMessages {
		return normalized
	}
	return cloneMessages(normalized[len(normalized)-sessionTranscriptMessages:])
}

func appendAssistantMessage(messages []*pb.ChatMessage, response string) []*pb.ChatMessage {
	text := strings.TrimSpace(response)
	if text == "" {
		return trimMessages(messages)
	}
	next := cloneMessages(messages)
	next = append(next, &pb.ChatMessage{Role: "assistant", Content: text})
	return trimMessages(next)
}

func requestSuppliesTranscript(messages []*pb.ChatMessage) bool {
	if len(messages) > 1 {
		return true
	}
	for _, message := range messages {
		if message == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(message.GetRole())) != "user" {
			return true
		}
	}
	return false
}

func (s *orchestratorServer) getTranscript(sessionID string) []*pb.ChatMessage {
	s.transcriptMu.Lock()
	defer s.transcriptMu.Unlock()
	if s.transcriptBySession == nil {
		return nil
	}
	return cloneMessages(s.transcriptBySession[sessionID])
}

func (s *orchestratorServer) setTranscript(sessionID string, messages []*pb.ChatMessage) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	trimmed := trimMessages(messages)
	s.transcriptMu.Lock()
	defer s.transcriptMu.Unlock()
	if len(trimmed) == 0 {
		if s.transcriptBySession != nil {
			delete(s.transcriptBySession, sessionID)
		}
		return
	}
	if s.transcriptBySession == nil {
		s.transcriptBySession = make(map[string][]*pb.ChatMessage)
	}
	s.transcriptBySession[sessionID] = trimmed
}

func (s *orchestratorServer) resolveMessages(sessionID string, incoming []*pb.ChatMessage) []*pb.ChatMessage {
	normalized := normalizeMessages(incoming)
	if len(normalized) == 0 {
		return nil
	}
	if requestSuppliesTranscript(normalized) {
		return trimMessages(normalized)
	}
	stored := s.getTranscript(sessionID)
	if len(stored) == 0 {
		return trimMessages(normalized)
	}
	combined := cloneMessages(stored)
	combined = append(combined, cloneMessages(normalized)...)
	return trimMessages(combined)
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	cloned := make([]byte, len(raw))
	copy(cloned, raw)
	return json.RawMessage(cloned)
}

func mergePendingToolCall(pending pendingToolState, resumed toolCall) (toolCall, bool, error) {
	merged, ok, err := orchtools.MergePendingCall(
		pending.Tool,
		pending.Args,
		pending.Missing,
		toPlannedCall(resumed),
	)
	if err != nil || !ok {
		return toolCall{}, ok, err
	}
	return fromPlannedCall(merged), true, nil
}

func fromPlannedCall(call orchtools.PlannedCall) toolCall {
	return toolCall{Tool: call.Action, Args: call.Args}
}

func toPlannedCall(call toolCall) orchtools.PlannedCall {
	return orchtools.PlannedCall{Action: call.Tool, Args: call.Args}
}

func (s *orchestratorServer) getPending(sessionID string) (pendingToolState, bool) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.pendingBySession == nil {
		return pendingToolState{}, false
	}
	state, ok := s.pendingBySession[sessionID]
	return state, ok
}

func (s *orchestratorServer) setPending(sessionID string, state pendingToolState) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.pendingBySession == nil {
		s.pendingBySession = make(map[string]pendingToolState)
	}
	s.pendingBySession[sessionID] = state
}

func (s *orchestratorServer) clearPending(sessionID string) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.pendingBySession == nil {
		return
	}
	delete(s.pendingBySession, sessionID)
}

func parseToolCalls(text string) ([]toolCall, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, nil
	}
	var calls []toolCall
	if err := json.Unmarshal([]byte(trimmed), &calls); err != nil {
		return nil, err
	}
	for i := range calls {
		if strings.TrimSpace(calls[i].Tool) == "" {
			return nil, fmt.Errorf("tool call %d missing tool name", i)
		}
		if len(calls[i].Args) == 0 {
			calls[i].Args = json.RawMessage(`{}`)
		}
	}
	return calls, nil
}

func supportedTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "get_time", "weather", "older_sister", "calculator":
		return true
	default:
		return false
	}
}

func filterSupportedToolCalls(calls []toolCall) []toolCall {
	filtered := calls[:0]
	for _, call := range calls {
		if supportedTool(call.Tool) {
			filtered = append(filtered, call)
		}
	}
	return filtered
}

func toolNames(calls []toolCall) []string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		names = append(names, call.Tool)
	}
	return names
}

func readGrammarFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type historyEntry struct {
	Timestamp  string   `json:"timestamp"`
	SessionID  string   `json:"session_id"`
	UserQuery  string   `json:"user_query"`
	Decision   string   `json:"decision"`
	Tools      []string `json:"tools"`
	ToolResult string   `json:"tool_result"`
	Response   string   `json:"response"`
	Status     string   `json:"status"`
	Error      string   `json:"error,omitempty"`
}

func (s *orchestratorServer) appendHistory(entry *historyEntry) {
	if s.cfg.HistoryDir == "" {
		return
	}
	month := time.Now().Format("2006-01")
	path := fmt.Sprintf("%s/history-%s.jsonl", s.cfg.HistoryDir, month)

	data, err := json.Marshal(entry)
	if err != nil {
		fmt.Printf("orch.history status=error err=%v\n", err)
		return
	}
	data = append(data, '\n')

	s.historyMu.Lock()
	defer s.historyMu.Unlock()

	if err := os.MkdirAll(s.cfg.HistoryDir, 0755); err != nil {
		fmt.Printf("orch.history status=error err=%v\n", err)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Printf("orch.history status=error err=%v\n", err)
		return
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		fmt.Printf("orch.history status=error err=%v\n", err)
	}
}

func main() {
	fmt.Println("eve-orchestrator: starting")

	cfg := config.Load()
	var routeDB *sql.DB
	if strings.TrimSpace(cfg.DatabaseURL) != "" {
		db, err := orchestrdb.OpenAndMigrate(cfg.DatabaseURL, cfg.DBMigrationsDir)
		if err != nil {
			fmt.Printf("orchestrator status=error database_url=%q err=%v\n", cfg.DatabaseURL, err)
			return
		}
		defer db.Close()
		routeDB = db
		fmt.Printf("orchestrator status=database_ok migrations_dir=%s\n", cfg.DBMigrationsDir)
	}

	orchAddr := ":5013"
	if cfg.OrchAddr != "" {
		orchAddr = cfg.OrchAddr
	}
	lis, err := net.Listen("tcp", orchAddr)
	if err != nil {
		fmt.Printf("orchestrator status=error listen_addr=%s err=%v\n", orchAddr, err)
		return
	}

	grpcServer := grpc.NewServer()
	selector := routing.NewSelectorWithDB(cfg.RoutesPath, cfg.EmbeddingHTTPURL, cfg.EmbeddingModel, cfg.RouteTopK, cfg.RouteDomainTopK, routeDB)
	if selector.Enabled() {
		timeout := time.Duration(cfg.EmbeddingTimeoutMs) * time.Millisecond
		if err := selector.Warmup(timeout); err != nil {
			fmt.Printf("orchestrator status=error route_warmup err=%v\n", err)
			return
		}
		fmt.Printf("orchestrator status=route_warmup_ok routes_path=%s\n", cfg.RoutesPath)
	}
	pb.RegisterOrchestratorServer(grpcServer, &orchestratorServer{
		cfg: cfg,
		tools: orchtools.NewLocalExecutorWithConfigs(orchtools.WeatherConfig{
			HTTPURL:           cfg.WeatherHTTPURL,
			GeocodingURL:      cfg.WeatherGeocodingURL,
			Latitude:          cfg.WeatherLatitude,
			Longitude:         cfg.WeatherLongitude,
			Timezone:          cfg.WeatherTimezone,
			LocationName:      cfg.WeatherLocationName,
			TemperatureUnit:   cfg.WeatherTemperatureUnit,
			WindSpeedUnit:     cfg.WeatherWindSpeedUnit,
			PrecipitationUnit: cfg.WeatherPrecipitationUnit,
		}, orchtools.OlderSisterConfig{
			APIKey:    cfg.OlderSisterAPIKey,
			HTTPURL:   cfg.OlderSisterHTTPURL,
			Model:     cfg.OlderSisterModel,
			TimeoutMs: cfg.OlderSisterTimeoutMs,
			WebSearch: cfg.OlderSisterWebSearch,
		}),
		routeSelector: selector,
	})
	fmt.Printf("orchestrator status=listening addr=%s\n", orchAddr)
	if err := grpcServer.Serve(lis); err != nil {
		fmt.Printf("orchestrator status=error err=%v\n", err)
	}
}
