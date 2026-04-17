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
	"eve-beemo/src/orchestrator/factsel"
	"eve-beemo/src/orchestrator/llm"
	"eve-beemo/src/orchestrator/memoryctx"
	"eve-beemo/src/orchestrator/prompts"
	"eve-beemo/src/orchestrator/routing"
	"eve-beemo/src/orchestrator/subjectctx"
	orchtools "eve-beemo/src/orchestrator/tools"
	"google.golang.org/grpc"
)

type orchestratorServer struct {
	pb.UnimplementedOrchestratorServer
	cfg                 config.Config
	tools               orchtools.Executor
	routeSelector       routeSelector
	factSelector        factSelector
	readGrammar         func(path string) (string, error)
	callCompletion      func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error)
	callFinalMessage    func(httpURL, model, prompt string, timeout time.Duration) (string, error)
	memoryMu            sync.Mutex
	memoryStore         *memoryctx.Store
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
	SubjectID         string          `json:"subject_id,omitempty"`
}

type routeSelector interface {
	Retrieve(query string, timeout time.Duration) ([]routing.Candidate, error)
}

type routeCatalog interface {
	Routes() []routing.Route
}

type factSelector interface {
	Select(query string, attrs []string, timeout time.Duration) (string, error)
	Attrs() []string
	Fact(attr string) (factsel.Fact, bool)
	QuestionPrompt(attrs []string) string
}

const (
	contextSelectionMessages  = 24
	activeContextTurns        = 6
	sessionTranscriptMessages = 24
	memoryRecallMinScore      = 0.48
	memoryRecallMinMargin     = 0.03
)

func useCurrentTurnGrounding(call toolCall, currentSubjectID string) bool {
	if strings.TrimSpace(currentSubjectID) == "" || strings.TrimSpace(call.Tool) != "calculator" {
		return false
	}

	args := map[string]json.RawMessage{}
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return false
		}
	}

	var operation string
	if err := json.Unmarshal(args["operation"], &operation); err != nil {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "bmi", "bmr", "tdee":
		return true
	default:
		return false
	}
}

func explicitObservationAttrs(text string) map[string]struct{} {
	patch, ok, err := orchtools.ExtractCalculatorObservationPatch(text)
	if err != nil || !ok {
		return nil
	}
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(patch, &values); err != nil {
		return nil
	}
	attrs := make(map[string]struct{}, len(values))
	for attr, raw := range values {
		if len(raw) == 0 {
			continue
		}
		attrs[attr] = struct{}{}
	}
	return attrs
}

func conflictingMemoryAttrs(conflicts map[string][]memoryctx.Observation, explicitAttrs map[string]struct{}, orderedAttrs []string) []string {
	if len(conflicts) == 0 {
		return nil
	}
	selected := make([]string, 0, len(conflicts))
	seen := map[string]struct{}{}
	for _, attr := range orderedAttrs {
		if _, ok := conflicts[attr]; !ok {
			continue
		}
		if _, explicit := explicitAttrs[attr]; explicit {
			continue
		}
		selected = append(selected, attr)
		seen[attr] = struct{}{}
	}
	for attr := range conflicts {
		if _, explicit := explicitAttrs[attr]; explicit {
			continue
		}
		if _, ok := seen[attr]; ok {
			continue
		}
		selected = append(selected, attr)
	}
	return selected
}

func filteredSnapshot(snapshot map[string]json.RawMessage, excluded []string) map[string]json.RawMessage {
	if len(snapshot) == 0 {
		return nil
	}
	blocked := make(map[string]struct{}, len(excluded))
	for _, attr := range excluded {
		blocked[attr] = struct{}{}
	}
	filtered := make(map[string]json.RawMessage, len(snapshot))
	for attr, raw := range snapshot {
		if _, blockedAttr := blocked[attr]; blockedAttr {
			continue
		}
		filtered[attr] = cloneRawMessage(raw)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func lookupAttributeFromTool(call toolCall, route routing.Route) (string, error) {
	args := map[string]json.RawMessage{}
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return "", fmt.Errorf("invalid %s args: %w", call.Tool, err)
		}
	}
	if raw, ok := args["attribute"]; ok {
		var attr string
		if err := json.Unmarshal(raw, &attr); err != nil {
			return "", fmt.Errorf("invalid memory_lookup attribute: %w", err)
		}
		attr = strings.TrimSpace(attr)
		if attr != "" {
			return attr, nil
		}
	}
	if value, ok := route.DefaultArgs["attribute"]; ok {
		if attr, ok := value.(string); ok && strings.TrimSpace(attr) != "" {
			return strings.TrimSpace(attr), nil
		}
	}
	return "", nil
}

func memoryLookupArgs(attr string) json.RawMessage {
	if strings.TrimSpace(attr) == "" {
		return json.RawMessage(`{}`)
	}
	raw, err := json.Marshal(map[string]string{"attribute": attr})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func buildMemoryLookupResult(memoryStore *memoryctx.Store, facts factSelector, subjectID, userQuery string, call toolCall, route routing.Route, timeout time.Duration) (orchtools.Result, error) {
	if strings.TrimSpace(subjectID) == "" {
		return orchtools.Result{
			Action:   call.Tool,
			Status:   "needs_input",
			Missing:  []string{"subject"},
			Question: "Who is this about?",
		}, nil
	}

	attr, err := lookupAttributeFromTool(call, route)
	if err != nil {
		return orchtools.Result{}, err
	}
	if attr == "" {
		matches, err := memoryStore.Recall(subjectID, userQuery, 3, timeout)
		if err != nil {
			return orchtools.Result{}, err
		}
		if match, ok := bestRecallMatch(matches); ok {
			return resultFromObservation(memoryStore, facts, subjectID, call.Tool, match.Observation)
		}
		question := "What detail should I look up?"
		if facts != nil {
			if selectedAttr, err := facts.Select(userQuery, route.Memory.Attrs, timeout); err == nil && strings.TrimSpace(selectedAttr) != "" {
				observation, ok, err := memoryStore.LookupAttribute(subjectID, selectedAttr)
				if err != nil {
					return orchtools.Result{}, err
				}
				if ok {
					return resultFromObservation(memoryStore, facts, subjectID, call.Tool, observation)
				}
			}
			question = facts.QuestionPrompt(route.Memory.Attrs)
		}
		return orchtools.Result{
			Action:   call.Tool,
			Status:   "needs_input",
			Missing:  []string{"detail"},
			Question: question,
		}, nil
	}
	observation, ok, err := memoryStore.LookupAttribute(subjectID, attr)
	if err != nil {
		return orchtools.Result{}, err
	}
	if !ok {
		return orchtools.Result{
			Action:   call.Tool,
			Status:   "needs_input",
			Missing:  []string{attr},
			Question: orchtools.ClarificationQuestion([]string{attr}),
		}, nil
	}
	return resultFromObservation(memoryStore, facts, subjectID, call.Tool, observation)
}

func resultFromObservation(memoryStore *memoryctx.Store, facts factSelector, subjectID, toolName string, observation memoryctx.Observation) (orchtools.Result, error) {
	attr := strings.TrimSpace(observation.Attribute)
	if attr != "" {
		latest, ok, err := memoryStore.LookupAttribute(subjectID, attr)
		if err != nil {
			return orchtools.Result{}, err
		}
		if ok {
			observation = latest
		}
	}
	value := observation.RawValue
	if len(value) == 0 {
		value = observation.CanonicalValue
	}
	outputLabel := strings.ReplaceAll(attr, "_", " ")
	kind := "text"
	if facts != nil {
		if fact, ok := facts.Fact(attr); ok {
			if strings.TrimSpace(fact.OutputLabel) != "" {
				outputLabel = strings.TrimSpace(fact.OutputLabel)
			}
			if strings.TrimSpace(fact.Kind) != "" {
				kind = strings.TrimSpace(fact.Kind)
			}
		}
	}
	if len(value) > 0 {
		formatted, err := orchtools.FormatFactValue(kind, value)
		if err == nil {
			return orchtools.Result{
				Action: toolName,
				Output: fmt.Sprintf("%s %s", outputLabel, formatted),
			}, nil
		}
	}
	text := strings.TrimSpace(observation.ObservationText)
	if text == "" {
		text = strings.TrimSpace(observation.SourceTurn)
	}
	if text == "" {
		text = "I don't have a clear remembered detail for that yet."
	}
	return orchtools.Result{
		Action: toolName,
		Output: text,
	}, nil
}

func bestRecallMatch(matches []memoryctx.RecallMatch) (memoryctx.RecallMatch, bool) {
	if len(matches) == 0 {
		return memoryctx.RecallMatch{}, false
	}
	best := matches[0]
	if best.Score < memoryRecallMinScore {
		return memoryctx.RecallMatch{}, false
	}
	bestKey := recallMatchKey(best.Observation)
	secondScore := float32(-2)
	for idx := 1; idx < len(matches); idx++ {
		if recallMatchKey(matches[idx].Observation) == bestKey {
			continue
		}
		secondScore = matches[idx].Score
		break
	}
	if secondScore > -2 && best.Score-secondScore < memoryRecallMinMargin {
		return memoryctx.RecallMatch{}, false
	}
	return best, true
}

func recallMatchKey(observation memoryctx.Observation) string {
	value := observation.CanonicalValue
	if len(value) == 0 {
		value = observation.RawValue
	}
	return strings.TrimSpace(observation.Attribute) + "::" + string(value)
}

func (s *orchestratorServer) Chat(ctx context.Context, req *pb.ChatRequest) (*pb.ChatResponse, error) {
	start := time.Now()
	effectiveMessages := s.resolveMessages(req.GetSessionId(), req.GetMessages())
	userQuery := latestUserQuery(req.GetMessages())
	if userQuery == "" {
		userQuery = latestUserQuery(effectiveMessages)
	}
	fmt.Printf("orch.chat start session=%s user_query=%q\n", req.GetSessionId(), userQuery)
	if userQuery == "" {
		fmt.Printf("orch.chat done session=%s status=empty_query ms=%d\n", req.GetSessionId(), time.Since(start).Milliseconds())
		s.appendHistory(&historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			UserQuery: userQuery,
			Status:    "empty_query",
		})
		return &pb.ChatResponse{Text: ""}, nil
	}

	if s.cfg.LLMHTTPURL == "" {
		fmt.Printf("orch.chat done session=%s status=error reason=missing_llm_url ms=%d\n", req.GetSessionId(), time.Since(start).Milliseconds())
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
	grammar, gerr := readGrammar(s.cfg.DecisionGrammarPath)
	if gerr != nil {
		fmt.Printf("orch.chat done session=%s status=error reason=grammar_read err=%v\n", req.GetSessionId(), gerr)
		s.appendHistory(&historyEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			SessionID: req.GetSessionId(),
			UserQuery: userQuery,
			Status:    "error",
			Error:     fmt.Sprintf("grammar_read: %v", gerr),
		})
		return nil, gerr
	}

	callCompletion := s.callCompletion
	if callCompletion == nil {
		callCompletion = llm.CallChatWithGrammar
	}
	callTimeout := time.Duration(s.cfg.LLMTimeoutMs) * time.Millisecond
	embeddingTimeout := time.Duration(s.cfg.EmbeddingTimeoutMs) * time.Millisecond
	memoryStore := s.getMemoryStore()
	activeContext := chatctx.Build(effectiveMessages, contextSelectionMessages, activeContextTurns)
	seededSubjects, aliasErr := memoryStore.LoadSubjectAliases()
	if aliasErr != nil {
		fmt.Printf("orch.chat done session=%s status=error reason=subject_alias_load ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), aliasErr)
		return nil, aliasErr
	}
	subjectContext := subjectctx.ResolveWithSeed(effectiveMessages, seededSubjects)
	subjectSummary := subjectContext.Summary()
	routingQuery := strings.TrimSpace(activeContext.UserEvidence)
	if routingQuery == "" {
		routingQuery = userQuery
	}

	text := ""
	fromPending := false
	pending, hasPending := s.getPending(req.GetSessionId())
	currentSubjectID := subjectContext.CurrentSubjectID
	if currentSubjectID == "" && hasPending && strings.TrimSpace(pending.SubjectID) != "" {
		currentSubjectID = strings.TrimSpace(pending.SubjectID)
		if strings.TrimSpace(subjectSummary) == "" {
			subjectSummary = "current_subject_id: " + currentSubjectID
		}
	}
	originQuery := userQuery
	if hasPending && strings.TrimSpace(pending.OriginalUserQuery) != "" {
		originQuery = pending.OriginalUserQuery
	}
	if err := memoryStore.RememberSubjectAliases(req.GetSessionId(), subjectContext.Subjects); err != nil {
		fmt.Printf("orch.chat subject_alias_ingest session=%s status=error err=%v\n", req.GetSessionId(), err)
	}
	if err := memoryStore.RememberUserMessage(req.GetSessionId(), currentSubjectID, userQuery); err != nil {
		fmt.Printf("orch.chat memory_ingest session=%s status=error err=%v\n", req.GetSessionId(), err)
	}
	if hasPending {
		if filledCall, ok, ferr := orchtools.TryFillPending(orchtools.PendingFillRequest{
			Action:  pending.Tool,
			Args:    pending.Args,
			Missing: pending.Missing,
			Reply:   userQuery,
		}); ferr != nil {
			fmt.Printf("orch.chat done session=%s status=error reason=resume_fill ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), ferr)
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
		resumePrompt := prompts.ResumeToolUpdate(
			pending.OriginalUserQuery,
			activeContext.Transcript,
			subjectSummary,
			pending.Tool,
			string(pending.Args),
			pending.Missing,
			pending.Question,
			userQuery,
		)
		resumeText, rerr := callCompletion(s.cfg.LLMHTTPURL, s.cfg.LLMModel, resumePrompt, grammar, callTimeout)
		if rerr != nil {
			fmt.Printf("orch.chat done session=%s status=error reason=llm_resume ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), rerr)
			s.appendHistory(&historyEntry{
				Timestamp: time.Now().Format(time.RFC3339),
				SessionID: req.GetSessionId(),
				UserQuery: userQuery,
				Status:    "error",
				Error:     fmt.Sprintf("llm_resume: %v", rerr),
			})
			return nil, rerr
		}
		resumeCalls, perr := parseToolCalls(resumeText)
		if perr != nil {
			fmt.Printf("orch.chat done session=%s status=error reason=resume_parse ms=%d err=%v raw=%q\n", req.GetSessionId(), time.Since(start).Milliseconds(), perr, resumeText)
			s.appendHistory(&historyEntry{
				Timestamp: time.Now().Format(time.RFC3339),
				SessionID: req.GetSessionId(),
				UserQuery: userQuery,
				Decision:  resumeText,
				Status:    "error",
				Error:     fmt.Sprintf("resume_parse: %v", perr),
			})
			return nil, perr
		}
		if len(resumeCalls) == 1 {
			mergedCall, ok, merr := mergePendingToolCall(pending, resumeCalls[0])
			if merr != nil {
				fmt.Printf("orch.chat resume_rejected session=%s err=%v raw=%q\n", req.GetSessionId(), merr, resumeText)
			} else if ok {
				mergedText, jerr := json.Marshal([]toolCall{mergedCall})
				if jerr != nil {
					return nil, jerr
				}
				text = string(mergedText)
				fromPending = true
			}
		}
		if strings.TrimSpace(text) == "" {
			s.setTranscript(req.GetSessionId(), appendAssistantMessage(effectiveMessages, pending.Question))
			s.appendHistory(&historyEntry{
				Timestamp: time.Now().Format(time.RFC3339),
				SessionID: req.GetSessionId(),
				UserQuery: userQuery,
				Decision:  resumeText,
				Tools:     []string{pending.Tool},
				Response:  pending.Question,
				Status:    "needs_input",
			})
			fmt.Printf("orch.chat done session=%s status=needs_input_resume question=%q ms=%d\n", req.GetSessionId(), pending.Question, time.Since(start).Milliseconds())
			return &pb.ChatResponse{Text: pending.Question}, nil
		}
	}

	var routeCandidates []routing.Candidate
	var catalogRoutes []routing.Route
	if selector, ok := s.routeSelector.(routeCatalog); ok {
		catalogRoutes = selector.Routes()
	}
	if strings.TrimSpace(text) == "" {
		prompt := prompts.ToolDecision(userQuery, activeContext.Transcript, subjectSummary)
		if s.routeSelector != nil {
			candidates, rerr := s.routeSelector.Retrieve(routingQuery, embeddingTimeout)
			if rerr != nil {
				fmt.Printf("orch.chat route_retrieval session=%s status=fallback err=%v\n", req.GetSessionId(), rerr)
			} else if len(candidates) > 0 {
				routeCandidates = candidates
				routeBlock := routing.FormatCandidates(candidates)
				fmt.Printf("orch.chat route_candidates session=%s routes=%v\n", req.GetSessionId(), candidateIDs(candidates))
				prompt = prompts.RoutedToolDecision(userQuery, activeContext.Transcript, subjectSummary, routeBlock)
			}
		}
		var err error
		text, err = callCompletion(s.cfg.LLMHTTPURL, s.cfg.LLMModel, prompt, grammar, callTimeout)
		if err != nil {
			fmt.Printf("orch.chat done session=%s status=error reason=llm_call ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), err)
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
		fmt.Printf("orch.chat done session=%s status=error reason=tool_parse ms=%d err=%v raw=%q\n", req.GetSessionId(), time.Since(start).Milliseconds(), err, text)
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
		retryPrompt := prompts.RetryToolDecision(userQuery, activeContext.Transcript, subjectSummary)
		retryText, rerr := callCompletion(s.cfg.LLMHTTPURL, s.cfg.LLMModel, retryPrompt, grammar, callTimeout)
		if rerr != nil {
			fmt.Printf("orch.chat retry_decision session=%s status=skipped err=%v\n", req.GetSessionId(), rerr)
		} else if retryCalls, perr := parseToolCalls(retryText); perr != nil {
			fmt.Printf("orch.chat retry_decision session=%s status=parse_error err=%v raw=%q\n", req.GetSessionId(), perr, retryText)
		} else if len(retryCalls) > 0 {
			toolCalls = retryCalls
			text = retryText
		}
	}
	toolsRequested := toolNames(toolCalls)
	fmt.Printf("orch.chat decision_raw session=%s text=%s tools=%v\n", req.GetSessionId(), text, toolsRequested)
	toolResult := ""
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
			groundingEvidence := evidenceText
			if useCurrentTurnGrounding(tool, currentSubjectID) {
				groundingEvidence = userQuery
			}
			groundedTool, gerr := orchtools.GroundCall(groundingEvidence, toPlannedCall(tool))
			if gerr != nil {
				fmt.Printf("orch.chat done session=%s status=error reason=tool_grounding ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), gerr)
				return nil, gerr
			}
			explicitTool = fromPlannedCall(groundedTool)
		}
		matchedRoute, matched, merr := routing.MatchCall(routeCandidates, catalogRoutes, explicitTool.Tool, explicitTool.Args)
		if merr != nil {
			fmt.Printf("orch.chat done session=%s status=error reason=route_match ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), merr)
			return nil, merr
		}
		memoryAttrs := []string(nil)
		memoryRead := false
		memoryWrite := false
		if matched {
			memoryAttrs = append(memoryAttrs, matchedRoute.Memory.Attrs...)
			memoryRead = matchedRoute.Memory.Read
			memoryWrite = matchedRoute.Memory.Write
		}
		if explicitTool.Tool == "memory_lookup" {
			if len(memoryAttrs) == 0 && s.factSelector != nil {
				memoryAttrs = s.factSelector.Attrs()
			}
			attr, aerr := lookupAttributeFromTool(explicitTool, matchedRoute)
			if aerr != nil {
				fmt.Printf("orch.chat done session=%s status=error reason=memory_lookup_attr ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), aerr)
				return nil, aerr
			}
			if attr != "" {
				memoryAttrs = []string{attr}
				explicitTool.Args = memoryLookupArgs(attr)
			} else {
				memoryAttrs = nil
				explicitTool.Args = json.RawMessage(`{}`)
			}
		}
		if currentSubjectID != "" && memoryWrite {
			if err := memoryStore.RememberUserMessageWithContext(req.GetSessionId(), currentSubjectID, userQuery, memoryctx.RecordContext{
				Domain:     matchedRoute.Domain,
				Route:      matchedRoute.ID,
				SourceTurn: userQuery,
				SourceType: memoryctx.SourceTypeExplicitUser,
			}, memoryAttrs...); err != nil {
				fmt.Printf("orch.chat memory_ingest session=%s status=error err=%v\n", req.GetSessionId(), err)
			}
		}
		snapshot := map[string]json.RawMessage(nil)
		snapshotDetails := memoryctx.SnapshotDetails{}
		if currentSubjectID != "" && memoryRead && len(memoryAttrs) > 0 {
			snapshotDetails = memoryStore.SnapshotDetails(req.GetSessionId(), currentSubjectID, memoryAttrs...)
			if snapshotDetails.Err != nil {
				fmt.Printf("orch.chat done session=%s status=error reason=memory_snapshot ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), snapshotDetails.Err)
				return nil, snapshotDetails.Err
			}
			snapshot = snapshotDetails.Values
		}
		if currentSubjectID != "" && memoryRead && len(memoryAttrs) > 0 {
			conflictAttrs := conflictingMemoryAttrs(snapshotDetails.Conflicts, explicitObservationAttrs(userQuery), memoryAttrs)
			if len(conflictAttrs) > 0 {
				pendingArgs := cloneRawMessage(explicitTool.Args)
				if explicitTool.Tool == "calculator" {
					provisionalTool, rerr := orchtools.ResolveCalculatorCall(
						toPlannedCall(explicitTool),
						userQuery,
						filteredSnapshot(snapshot, conflictAttrs),
					)
					if rerr != nil {
						fmt.Printf("orch.chat done session=%s status=error reason=tool_resolve_conflict ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), rerr)
						return nil, rerr
					}
					pendingArgs = cloneRawMessage(provisionalTool.Args)
				}
				question := orchtools.ClarificationQuestion(conflictAttrs)
				s.setPending(req.GetSessionId(), pendingToolState{
					OriginalUserQuery: originQuery,
					Tool:              explicitTool.Tool,
					Args:              pendingArgs,
					Missing:           append([]string(nil), conflictAttrs...),
					Question:          question,
					SubjectID:         currentSubjectID,
				})
				s.setTranscript(req.GetSessionId(), appendAssistantMessage(effectiveMessages, question))
				s.appendHistory(&historyEntry{
					Timestamp:  time.Now().Format(time.RFC3339),
					SessionID:  req.GetSessionId(),
					UserQuery:  userQuery,
					Decision:   text,
					Tools:      toolsRequested,
					ToolResult: fmt.Sprintf("status=needs_input missing=%v question=%s", conflictAttrs, question),
					Response:   question,
					Status:     "needs_input",
				})
				fmt.Printf("orch.chat done session=%s status=needs_input tool=%s missing=%v reason=memory_conflict ms=%d\n", req.GetSessionId(), explicitTool.Tool, conflictAttrs, time.Since(start).Milliseconds())
				return &pb.ChatResponse{Text: question}, nil
			}
		}
		resolvedTool := toPlannedCall(explicitTool)
		if explicitTool.Tool == "calculator" {
			var rerr error
			resolvedTool, rerr = orchtools.ResolveCalculatorCall(
				toPlannedCall(explicitTool),
				userQuery,
				snapshot,
			)
			if rerr != nil {
				fmt.Printf("orch.chat done session=%s status=error reason=tool_resolve ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), rerr)
				return nil, rerr
			}
			if currentSubjectID != "" && memoryWrite {
				if err := memoryStore.RememberToolCallWithContext(req.GetSessionId(), currentSubjectID, resolvedTool, memoryctx.RecordContext{
					Domain:     matchedRoute.Domain,
					Route:      matchedRoute.ID,
					SourceTurn: userQuery,
					SourceType: memoryctx.SourceTypeResolvedToolArgs,
				}, memoryAttrs...); err != nil {
					fmt.Printf("orch.chat memory_store session=%s status=error err=%v\n", req.GetSessionId(), err)
				}
			}
		}
		tool = fromPlannedCall(resolvedTool)
		fmt.Printf("orch.chat tool_call session=%s tool=%s args=%s\n", req.GetSessionId(), tool.Tool, string(tool.Args))
		var result orchtools.Result
		if tool.Tool == "memory_lookup" {
			result, err = buildMemoryLookupResult(memoryStore, s.factSelector, currentSubjectID, userQuery, tool, matchedRoute, embeddingTimeout)
		} else {
			result, err = s.tools.Execute(ctx, orchtools.Request{
				SessionID: req.GetSessionId(),
				Action:    tool.Tool,
				Args:      tool.Args,
			})
		}
		if err != nil {
			fmt.Printf("orch.chat done session=%s status=error reason=tool_call ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), err)
			s.appendHistory(&historyEntry{
				Timestamp: time.Now().Format(time.RFC3339),
				SessionID: req.GetSessionId(),
				UserQuery: userQuery,
				Decision:  text,
				Tools:     toolsRequested,
				Status:    "error",
				Error:     fmt.Sprintf("tool_call: %v", err),
			})
			return nil, err
		}
		if result.Status == "needs_input" {
			s.setPending(req.GetSessionId(), pendingToolState{
				OriginalUserQuery: originQuery,
				Tool:              tool.Tool,
				Args:              cloneRawMessage(tool.Args),
				Missing:           append([]string(nil), result.Missing...),
				Question:          result.Question,
				SubjectID:         currentSubjectID,
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
			fmt.Printf("orch.chat done session=%s status=needs_input tool=%s missing=%v ms=%d\n", req.GetSessionId(), result.Action, result.Missing, time.Since(start).Milliseconds())
			return &pb.ChatResponse{Text: result.Question}, nil
		}
		s.clearPending(req.GetSessionId())
		toolResult += fmt.Sprintf("tool=%s result=%s\n", result.Action, result.Output)
	}

	followup := prompts.FinalResponse(originQuery, userQuery, activeContext.Transcript, subjectSummary, text, toolResult)
	callFinalMessage := s.callFinalMessage
	if callFinalMessage == nil {
		callFinalMessage = llm.CallOnce
	}
	finalText, err := callFinalMessage(s.cfg.LLMHTTPURL, s.cfg.LLMModel, followup, time.Duration(s.cfg.LLMTimeoutMs)*time.Millisecond)
	if err != nil {
		fmt.Printf("orch.chat done session=%s status=error reason=llm_followup ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), err)
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

func (s *orchestratorServer) getMemoryStore() *memoryctx.Store {
	s.memoryMu.Lock()
	defer s.memoryMu.Unlock()
	if s.memoryStore == nil {
		s.memoryStore = memoryctx.NewStore().WithEmbeddings(
			s.cfg.EmbeddingHTTPURL,
			s.cfg.EmbeddingModel,
			time.Duration(s.cfg.EmbeddingTimeoutMs)*time.Millisecond,
		)
	}
	return s.memoryStore
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
	return toolCall{
		Tool: call.Action,
		Args: call.Args,
	}
}

func toPlannedCall(call toolCall) orchtools.PlannedCall {
	return orchtools.PlannedCall{
		Action: call.Tool,
		Args:   call.Args,
	}
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

func toolNames(calls []toolCall) []string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		names = append(names, call.Tool)
	}
	return names
}

func candidateIDs(candidates []routing.Candidate) []string {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.Route.ID)
	}
	return ids
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
		return
	}
}

func main() {
	fmt.Println("eve-orchestrator: starting")

	cfg := config.Load()
	var memoryStore *memoryctx.Store
	var routeDB *sql.DB
	if strings.TrimSpace(cfg.DatabaseURL) != "" {
		db, err := orchestrdb.OpenAndMigrate(cfg.DatabaseURL, cfg.DBMigrationsDir)
		if err != nil {
			fmt.Printf("orchestrator status=error database_url=%q err=%v\n", cfg.DatabaseURL, err)
			return
		}
		defer db.Close()
		routeDB = db
		memoryStore = memoryctx.NewPostgresStore(db).WithEmbeddings(
			cfg.EmbeddingHTTPURL,
			cfg.EmbeddingModel,
			time.Duration(cfg.EmbeddingTimeoutMs)*time.Millisecond,
		)
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
	facts := factsel.NewSelector(cfg.FactsPath, cfg.EmbeddingHTTPURL, cfg.EmbeddingModel)
	if selector.Enabled() {
		timeout := time.Duration(cfg.EmbeddingTimeoutMs) * time.Millisecond
		if err := selector.Warmup(timeout); err != nil {
			fmt.Printf("orchestrator status=error route_warmup err=%v\n", err)
			return
		}
		fmt.Printf("orchestrator status=route_warmup_ok routes_path=%s\n", cfg.RoutesPath)
	}
	if facts.Configured() {
		timeout := time.Duration(cfg.EmbeddingTimeoutMs) * time.Millisecond
		if err := facts.Warmup(timeout); err != nil {
			fmt.Printf("orchestrator status=error fact_warmup err=%v\n", err)
			return
		}
		fmt.Printf("orchestrator status=fact_warmup_ok facts_path=%s attrs=%v\n", cfg.FactsPath, facts.Attrs())
	}
	if memoryStore != nil {
		timeout := time.Duration(cfg.EmbeddingTimeoutMs) * time.Millisecond
		count, err := memoryStore.BackfillObservationEmbeddings(timeout)
		if err != nil {
			fmt.Printf("orchestrator status=error observation_backfill err=%v\n", err)
			return
		}
		fmt.Printf("orchestrator status=observation_backfill_ok rows=%d\n", count)
	}
	pb.RegisterOrchestratorServer(grpcServer, &orchestratorServer{
		cfg:           cfg,
		tools:         orchtools.NewLocalExecutor(),
		memoryStore:   memoryStore,
		routeSelector: selector,
		factSelector:  facts,
	})
	fmt.Printf("orchestrator status=listening addr=%s\n", orchAddr)
	if err := grpcServer.Serve(lis); err != nil {
		fmt.Printf("orchestrator status=error err=%v\n", err)
	}
}
