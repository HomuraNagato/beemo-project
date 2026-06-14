package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/chatctx"
	"eve-beemo/src/orchestrator/routing"
	orchtools "eve-beemo/src/orchestrator/tools"
)

const (
	decisionSourceNone          = "none"
	decisionSourceDeterministic = "deterministic"
	decisionSourcePendingFill   = "pending_fill"
	decisionSourcePendingLLM    = "pending_llm"
	decisionSourceRoutedLLM     = "routed_llm"
	decisionSourceGeneralLLM    = "general_llm"
	decisionSourceRetryLLM      = "retry_llm"
	responsePathNeedsInput      = "needs_input"
	responsePathDirect          = "direct"
	responsePathFinalLLM        = "final_llm"
)

type turnContext struct {
	SessionID         string
	EffectiveMessages []*pb.ChatMessage
	UserQuery         string
	OriginQuery       string
	ActiveContext     chatctx.ActiveContext
	RoutingQuery      string
	Pending           pendingToolState
	HasPending        bool
	DecisionText      string
	DecisionSource    string
	RouteCandidates   []routing.Candidate
	ToolCalls         []toolCall
	ToolsRequested    []string
	ToolResults       []orchtools.Result
	ToolResultText    string
	ResponseText      string
	ResponsePath      string
}

type turnTrace struct {
	SessionID       string   `json:"session_id"`
	UserQuery       string   `json:"user_query"`
	OriginQuery     string   `json:"origin_query,omitempty"`
	HasPending      bool     `json:"has_pending"`
	DecisionSource  string   `json:"decision_source"`
	Tools           []string `json:"tools,omitempty"`
	ToolResultCount int      `json:"tool_result_count"`
	ResponsePath    string   `json:"response_path,omitempty"`
	Status          string   `json:"status"`
	Error           string   `json:"error,omitempty"`
	ElapsedMs       int64    `json:"elapsed_ms"`
}

func newTurnContext(sessionID string, effectiveMessages []*pb.ChatMessage, userQuery string, pending pendingToolState, hasPending bool) *turnContext {
	activeContext := chatctx.Build(effectiveMessages, contextSelectionMessages, activeContextTurns)
	routingQuery := strings.TrimSpace(activeContext.UserEvidence)
	if routingQuery == "" {
		routingQuery = userQuery
	}
	originQuery := userQuery
	if hasPending && strings.TrimSpace(pending.OriginalUserQuery) != "" {
		originQuery = pending.OriginalUserQuery
	}
	return &turnContext{
		SessionID:         sessionID,
		EffectiveMessages: effectiveMessages,
		UserQuery:         userQuery,
		OriginQuery:       originQuery,
		ActiveContext:     activeContext,
		RoutingQuery:      routingQuery,
		Pending:           pending,
		HasPending:        hasPending,
		DecisionSource:    decisionSourceNone,
	}
}

func (tc *turnContext) setDecision(text, source string) {
	tc.DecisionText = text
	if strings.TrimSpace(source) != "" {
		tc.DecisionSource = source
	}
}

func (tc *turnContext) setToolCalls(calls []toolCall) {
	tc.ToolCalls = calls
	tc.ToolsRequested = toolNames(calls)
}

func (tc *turnContext) appendToolResult(result orchtools.Result) {
	tc.ToolResults = append(tc.ToolResults, result)
	tc.ToolResultText += fmt.Sprintf("tool=%s result=%s\n", result.Action, result.Output)
}

func (tc *turnContext) evidenceText(latestUserReply string) string {
	evidence := strings.TrimSpace(tc.ActiveContext.UserEvidence)
	if evidence != "" {
		return evidence
	}
	evidence = tc.OriginQuery
	if tc.HasPending && strings.TrimSpace(latestUserReply) != "" && latestUserReply != tc.OriginQuery {
		evidence += "\n" + latestUserReply
	}
	return evidence
}

func (tc *turnContext) logTrace(status string, elapsed time.Duration, err error) {
	trace := turnTrace{
		SessionID:       tc.SessionID,
		UserQuery:       tc.UserQuery,
		OriginQuery:     tc.OriginQuery,
		HasPending:      tc.HasPending,
		DecisionSource:  tc.DecisionSource,
		Tools:           append([]string(nil), tc.ToolsRequested...),
		ToolResultCount: len(tc.ToolResults),
		ResponsePath:    tc.ResponsePath,
		Status:          status,
		ElapsedMs:       elapsed.Milliseconds(),
	}
	if err != nil {
		trace.Error = err.Error()
	}
	data, marshalErr := json.Marshal(trace)
	if marshalErr != nil {
		fmt.Printf("orch.chat trace_marshal_error err=%v\n", marshalErr)
		return
	}
	fmt.Printf("orch.chat trace=%s\n", data)
}
