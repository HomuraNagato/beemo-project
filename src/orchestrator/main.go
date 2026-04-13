package main

import (
	"context"
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
	"eve-beemo/src/orchestrator/llm"
	"eve-beemo/src/orchestrator/prompts"
	orchtools "eve-beemo/src/orchestrator/tools"
	"google.golang.org/grpc"
)

type orchestratorServer struct {
	pb.UnimplementedOrchestratorServer
	cfg              config.Config
	tools            orchtools.Executor
	readGrammar      func(path string) (string, error)
	callCompletion   func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error)
	callFinalMessage func(httpURL, model, prompt string, timeout time.Duration) (string, error)
	historyMu        sync.Mutex
	pendingMu        sync.Mutex
	pendingBySession map[string]pendingToolState
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

const (
	contextSelectionMessages = 24
	activeContextTurns       = 6
)

func (s *orchestratorServer) Chat(ctx context.Context, req *pb.ChatRequest) (*pb.ChatResponse, error) {
	start := time.Now()
	userQuery := ""
	for i := len(req.GetMessages()) - 1; i >= 0; i-- {
		if req.GetMessages()[i].GetRole() == "user" {
			userQuery = req.GetMessages()[i].GetContent()
			break
		}
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
	activeContext := chatctx.Build(req.GetMessages(), contextSelectionMessages, activeContextTurns)

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

	if strings.TrimSpace(text) == "" {
		prompt := prompts.ToolDecision(userQuery, activeContext.Transcript)
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
		retryPrompt := prompts.RetryToolDecision(userQuery, activeContext.Transcript)
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
		if !fromPending {
			groundedTool, gerr := orchtools.GroundCall(evidenceText, toPlannedCall(tool))
			if gerr != nil {
				fmt.Printf("orch.chat done session=%s status=error reason=tool_grounding ms=%d err=%v\n", req.GetSessionId(), time.Since(start).Milliseconds(), gerr)
				return nil, gerr
			}
			tool = fromPlannedCall(groundedTool)
		}
		fmt.Printf("orch.chat tool_call session=%s tool=%s args=%s\n", req.GetSessionId(), tool.Tool, string(tool.Args))
		result, err := s.tools.Execute(ctx, orchtools.Request{
			SessionID: req.GetSessionId(),
			Action:    tool.Tool,
			Args:      tool.Args,
		})
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
			})
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

	followup := prompts.FinalResponse(originQuery, userQuery, activeContext.Transcript, text, toolResult)
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
	fmt.Printf("orch.chat done session=%s status=ok path=final ms=%d\n", req.GetSessionId(), time.Since(start).Milliseconds())
	return &pb.ChatResponse{Text: finalText}, nil
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
	pb.RegisterOrchestratorServer(grpcServer, &orchestratorServer{
		cfg:   cfg,
		tools: orchtools.NewLocalExecutor(),
	})
	fmt.Printf("orchestrator status=listening addr=%s\n", orchAddr)
	if err := grpcServer.Serve(lis); err != nil {
		fmt.Printf("orchestrator status=error err=%v\n", err)
	}
}
