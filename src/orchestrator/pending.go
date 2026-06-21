package main

import (
	"encoding/json"
	"strings"
	"time"

	"eve-beemo/src/orchestrator/prompts"
	orchtools "eve-beemo/src/orchestrator/tools"
)

type pendingResumeRequest struct {
	sessionID        string
	pending          pendingToolState
	userQuery        string
	activeTranscript string
	grammar          string
	callTimeout      time.Duration
	callCompletion   func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error)
}

type pendingDecision struct {
	Text        string
	FromPending bool
	Response    string
}

func (s *orchestratorServer) resumePendingDecision(req pendingResumeRequest) (pendingDecision, bool, bool, error) {
	if isPendingCancel(req.userQuery) {
		s.clearPending(req.sessionID)
		return pendingDecision{Response: "Canceled."}, true, true, nil
	}

	if filledCall, ok, err := orchtools.TryFillPending(orchtools.PendingFillRequest{
		Action:  req.pending.Tool,
		Args:    req.pending.Args,
		Missing: req.pending.Missing,
		Reply:   req.userQuery,
	}); err != nil {
		return pendingDecision{}, false, false, err
	} else if ok {
		filledText, jerr := json.Marshal([]toolCall{fromPlannedCall(filledCall)})
		if jerr != nil {
			return pendingDecision{}, false, false, jerr
		}
		return pendingDecision{Text: string(filledText), FromPending: true}, true, false, nil
	}

	if s.cfg.DeterministicToolShortcuts {
		if inferred, ok, err := orchtools.InferToolCall(req.userQuery); err != nil {
			return pendingDecision{}, false, false, err
		} else if ok && pendingInterruptedByNewCall(req.pending, inferred) {
			s.clearPending(req.sessionID)
			inferredText, jerr := json.Marshal([]toolCall{fromPlannedCall(inferred)})
			if jerr != nil {
				return pendingDecision{}, false, true, jerr
			}
			return pendingDecision{Text: string(inferredText)}, true, true, nil
		}
	}

	resumePrompt := prompts.ResumeToolUpdate(
		req.pending.OriginalUserQuery,
		req.activeTranscript,
		req.pending.Tool,
		string(req.pending.Args),
		req.pending.Missing,
		req.pending.Question,
		req.userQuery,
	)
	resumeText, err := req.callCompletion(s.cfg.LLMDecisionHTTPURL, s.cfg.LLMModel, resumePrompt, req.grammar, req.callTimeout)
	if err != nil {
		return pendingDecision{}, false, false, err
	}
	resumeCalls, err := parseToolCalls(resumeText)
	if err != nil {
		return pendingDecision{}, false, false, err
	}
	resumeCalls = filterSupportedToolCalls(resumeCalls)
	if len(resumeCalls) == 1 {
		mergedCall, ok, err := mergePendingToolCall(req.pending, resumeCalls[0])
		if err != nil {
			return pendingDecision{}, false, false, err
		}
		if ok && pendingFieldsSatisfied(req.pending.Missing, mergedCall.Args) {
			mergedText, jerr := json.Marshal([]toolCall{mergedCall})
			if jerr != nil {
				return pendingDecision{}, false, false, jerr
			}
			return pendingDecision{Text: string(mergedText), FromPending: true}, true, false, nil
		}
	}

	s.clearPending(req.sessionID)
	return pendingDecision{}, false, true, nil
}

func pendingInterruptedByNewCall(pending pendingToolState, inferred orchtools.PlannedCall) bool {
	pendingTool := strings.TrimSpace(pending.Tool)
	inferredAction := strings.TrimSpace(inferred.Action)
	return pendingTool != "" && inferredAction != "" && pendingTool != inferredAction
}

func isPendingCancel(text string) bool {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(text)), ".!? ")
	switch normalized {
	case "cancel", "cancelled", "canceled", "stop", "escape", "esc", "scape", "nevermind", "never mind":
		return true
	default:
		return false
	}
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
