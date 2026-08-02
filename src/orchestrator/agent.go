package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/llm"
	orchtools "eve-beemo/src/orchestrator/tools"
)

type agentDecision struct {
	Type string          `json:"type"`
	Text string          `json:"text,omitempty"`
	Tool string          `json:"tool,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`
}

type agentObservation struct {
	Tool   string `json:"tool"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

func (s *orchestratorServer) RunAgent(req *pb.AgentRequest, stream pb.Orchestrator_RunAgentServer) error {
	mode := strings.ToLower(strings.TrimSpace(req.GetMode()))
	if mode == "" {
		mode = "chat"
	}
	if mode != "chat" && mode != "code" {
		return fmt.Errorf("unsupported agent mode %q", mode)
	}
	ctx := stream.Context()
	defer func() {
		if ctx.Err() != nil && s.agentStore != nil {
			statusCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = s.agentStore.UpdateSessionStatus(statusCtx, req.GetSessionId(), "cancelled")
		}
	}()
	status := "active"
	if err := s.saveAgentSession(ctx, req, mode, status); err != nil {
		s.log().Warn("orch.agent.session", "session", req.GetSessionId(), "err", err)
	}
	if s.agentStore != nil {
		userText := latestUserQuery(req.GetMessages())
		if strings.TrimSpace(userText) != "" {
			if err := s.agentStore.AppendEvent(ctx, req.GetSessionId(), "user", "", userText, nil); err != nil {
				s.log().Warn("orch.agent.user_event", "session", req.GetSessionId(), "err", err)
			}
		}
	}
	if err := s.sendAgentEvent(ctx, stream, req.GetSessionId(), "status", "", "thinking", nil, ""); err != nil {
		return err
	}

	if mode == "chat" {
		response, err := s.Chat(ctx, &pb.ChatRequest{
			SessionId: req.GetSessionId(),
			Messages:  req.GetMessages(),
			Options:   req.GetOptions(),
		})
		if err != nil {
			return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", err.Error(), nil, "")
		}
		if err := s.sendAgentEvent(ctx, stream, req.GetSessionId(), "assistant", "", response.GetText(), map[string]any{"tools": response.GetTools()}, ""); err != nil {
			return err
		}
		return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "complete", "", "complete", nil, "")
	}

	if strings.TrimSpace(req.GetWorkspace()) == "" {
		return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", "Select a repository before using Code mode.", nil, "")
	}
	if s.codeTools == nil {
		return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", "beemo-code is not configured", nil, "")
	}
	return s.runCodeAgent(req, stream)
}

func (s *orchestratorServer) runCodeAgent(req *pb.AgentRequest, stream pb.Orchestrator_RunAgentServer) error {
	ctx := stream.Context()
	observations := s.initialCodeObservations(ctx, req)
	for _, observation := range observations {
		_ = s.sendAgentEvent(ctx, stream, req.GetSessionId(), "tool_result", observation.Tool, observation.Output, observation, "")
	}

	for step := 1; step <= s.cfg.CodeMaxSteps; step++ {
		prompt := buildCodeAgentPrompt(req, observations, step, s.cfg.CodeMaxSteps)
		callAgent := s.callAgentCompletion
		if callAgent == nil {
			callAgent = llm.CallOnceWithMaxTokensContext
		}
		decisionText, err := callAgent(ctx, s.cfg.LLMHTTPURL, s.cfg.LLMModel, prompt, s.cfg.CodeMaxTokens, time.Duration(s.cfg.LLMTimeoutMs)*time.Millisecond)
		if err != nil {
			return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", fmt.Sprintf("coding model failed: %v", err), nil, "")
		}
		decision, err := parseAgentDecision(decisionText)
		if err != nil {
			observations = append(observations, agentObservation{Tool: "agent", Error: "invalid decision: " + err.Error()})
			continue
		}

		switch decision.Type {
		case "final":
			text := strings.TrimSpace(decision.Text)
			if text == "" {
				text = "The coding task completed without a final summary."
			}
			if err := s.sendAgentEvent(ctx, stream, req.GetSessionId(), "assistant", "", text, nil, ""); err != nil {
				return err
			}
			_ = s.saveAgentSession(ctx, req, "code", "complete")
			return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "complete", "", "complete", nil, "")
		case "tool":
			if strings.TrimSpace(decision.Tool) == "" {
				observations = append(observations, agentObservation{Tool: "agent", Error: "tool decision omitted a tool name"})
				continue
			}
			if len(decision.Args) == 0 {
				decision.Args = json.RawMessage(`{}`)
			}
			if err := s.sendAgentEvent(ctx, stream, req.GetSessionId(), "tool_start", decision.Tool, "", json.RawMessage(decision.Args), ""); err != nil {
				return err
			}
			observation, err := s.executeAgentTool(ctx, req, stream, decision)
			if err != nil {
				observation = agentObservation{Tool: decision.Tool, Error: err.Error()}
			}
			observations = appendBoundedObservations(observations, observation, 18)
			if err := s.sendAgentEvent(ctx, stream, req.GetSessionId(), "tool_result", decision.Tool, observation.Output, observation, ""); err != nil {
				return err
			}
		default:
			observations = append(observations, agentObservation{Tool: "agent", Error: "decision type must be tool or final"})
		}
	}
	return s.sendAgentEvent(ctx, stream, req.GetSessionId(), "error", "", "Code mode reached its tool-step limit before completing.", nil, "")
}

func (s *orchestratorServer) executeAgentTool(ctx context.Context, req *pb.AgentRequest, stream pb.Orchestrator_RunAgentServer, decision agentDecision) (agentObservation, error) {
	if strings.HasPrefix(decision.Tool, "code.") {
		result, err := s.codeTools.Execute(ctx, &pb.CodeToolRequest{
			SessionId: req.GetSessionId(),
			Workspace: req.GetWorkspace(),
			Action:    decision.Tool,
			ArgsJson:  string(decision.Args),
		})
		if err != nil {
			return agentObservation{}, err
		}
		if result.GetStatus() == "approval_required" {
			approved, err := s.waitForApproval(ctx, stream, req.GetSessionId(), result.GetApprovalId(), decision.Tool, string(decision.Args), result.GetOutput())
			if err != nil {
				return agentObservation{}, err
			}
			if !approved {
				return agentObservation{Tool: decision.Tool, Error: "user denied approval"}, nil
			}
			result, err = s.codeTools.Execute(ctx, &pb.CodeToolRequest{
				SessionId: req.GetSessionId(), Workspace: req.GetWorkspace(), Action: decision.Tool,
				ArgsJson: string(decision.Args), Approved: true,
			})
			if err != nil {
				return agentObservation{}, err
			}
		}
		if result.GetStatus() != "ok" {
			return agentObservation{Tool: decision.Tool, Error: result.GetError()}, nil
		}
		if result.GetChanged() {
			_ = s.sendAgentEvent(ctx, stream, req.GetSessionId(), "file_change", decision.Tool, "workspace changed", nil, "")
		}
		return agentObservation{Tool: decision.Tool, Output: result.GetOutput()}, nil
	}

	if !supportedTool(decision.Tool) || decision.Tool == "beemo.direct" {
		return agentObservation{}, fmt.Errorf("unsupported agent tool %q", decision.Tool)
	}
	result, err := s.tools.Execute(ctx, orchtools.Request{SessionID: req.GetSessionId(), Action: decision.Tool, Args: decision.Args})
	if err != nil {
		return agentObservation{}, err
	}
	if result.Status == "needs_input" {
		return agentObservation{Tool: decision.Tool, Error: result.Question}, nil
	}
	return agentObservation{Tool: decision.Tool, Output: result.Output}, nil
}

func (s *orchestratorServer) initialCodeObservations(ctx context.Context, req *pb.AgentRequest) []agentObservation {
	requests := []struct {
		action string
		args   string
	}{
		{"code.list", `{"path":"."}`},
		{"code.git_status", `{}`},
		{"code.read", `{"path":"AGENTS.md","limit":32768}`},
	}
	result := make([]agentObservation, 0, len(requests))
	for _, item := range requests {
		response, err := s.codeTools.Execute(ctx, &pb.CodeToolRequest{
			SessionId: req.GetSessionId(), Workspace: req.GetWorkspace(), Action: item.action, ArgsJson: item.args,
		})
		if err != nil {
			result = append(result, agentObservation{Tool: item.action, Error: err.Error()})
			continue
		}
		if response.GetStatus() == "ok" {
			result = append(result, agentObservation{Tool: item.action, Output: response.GetOutput()})
		} else if item.action != "code.read" {
			result = append(result, agentObservation{Tool: item.action, Error: response.GetError()})
		}
	}
	return result
}

func (s *orchestratorServer) waitForApproval(ctx context.Context, stream pb.Orchestrator_RunAgentServer, sessionID, approvalID, tool, argsJSON, reason string) (bool, error) {
	pending := pendingApproval{sessionID: sessionID, decision: make(chan bool, 1)}
	s.approvalMu.Lock()
	if s.pendingApprovals == nil {
		s.pendingApprovals = map[string]pendingApproval{}
	}
	s.pendingApprovals[approvalID] = pending
	s.approvalMu.Unlock()
	defer func() {
		s.approvalMu.Lock()
		delete(s.pendingApprovals, approvalID)
		s.approvalMu.Unlock()
	}()
	if s.agentStore != nil {
		_ = s.agentStore.CreateApproval(ctx, approvalID, sessionID, tool, argsJSON)
	}
	if err := s.sendAgentEvent(ctx, stream, sessionID, "approval", tool, reason, json.RawMessage(argsJSON), approvalID); err != nil {
		return false, err
	}
	timeout := time.NewTimer(time.Duration(s.cfg.CodeApprovalTimeoutMs) * time.Millisecond)
	defer timeout.Stop()
	select {
	case approved := <-pending.decision:
		return approved, nil
	case <-timeout.C:
		return false, fmt.Errorf("approval timed out")
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (s *orchestratorServer) Approve(ctx context.Context, req *pb.ApprovalDecision) (*pb.ApprovalResult, error) {
	s.approvalMu.Lock()
	pending, ok := s.pendingApprovals[req.GetApprovalId()]
	s.approvalMu.Unlock()
	if !ok || pending.sessionID != req.GetSessionId() {
		return &pb.ApprovalResult{Accepted: false, Status: "not_pending"}, nil
	}
	select {
	case pending.decision <- req.GetApproved():
		if s.agentStore != nil {
			_ = s.agentStore.DecideApproval(ctx, req.GetApprovalId(), req.GetApproved())
		}
		return &pb.ApprovalResult{Accepted: true, Status: "accepted"}, nil
	default:
		return &pb.ApprovalResult{Accepted: false, Status: "already_decided"}, nil
	}
}

func (s *orchestratorServer) ListAgentWorkspaces(ctx context.Context, req *pb.ListWorkspacesRequest) (*pb.ListWorkspacesResponse, error) {
	if s.codeTools == nil {
		return &pb.ListWorkspacesResponse{}, nil
	}
	return s.codeTools.ListWorkspaces(ctx, req)
}

func (s *orchestratorServer) ListAgentSessions(ctx context.Context, req *pb.SessionListRequest) (*pb.SessionListResponse, error) {
	if s.agentStore == nil {
		return &pb.SessionListResponse{}, nil
	}
	sessions, err := s.agentStore.ListSessions(ctx, s.cfg.MemoryUserKey, int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	response := &pb.SessionListResponse{Sessions: make([]*pb.AgentSessionSummary, 0, len(sessions))}
	for _, session := range sessions {
		response.Sessions = append(response.Sessions, &pb.AgentSessionSummary{
			SessionId: session.SessionID, Mode: session.Mode, Workspace: session.Workspace,
			Status: session.Status, UpdatedUnixMs: session.UpdatedAt.UnixMilli(),
		})
	}
	return response, nil
}

func (s *orchestratorServer) GetAgentSession(ctx context.Context, req *pb.SessionRequest) (*pb.AgentSessionDetail, error) {
	if s.agentStore == nil {
		return nil, fmt.Errorf("agent session storage is unavailable")
	}
	session, events, err := s.agentStore.GetSession(ctx, s.cfg.MemoryUserKey, strings.TrimSpace(req.GetSessionId()))
	if err != nil {
		return nil, err
	}
	detail := &pb.AgentSessionDetail{Session: &pb.AgentSessionSummary{
		SessionId: session.SessionID, Mode: session.Mode, Workspace: session.Workspace,
		Status: session.Status, UpdatedUnixMs: session.UpdatedAt.UnixMilli(),
	}}
	for _, event := range events {
		detail.Events = append(detail.Events, &pb.AgentEvent{
			SessionId: session.SessionID, Type: event.EventType, Tool: event.Tool,
			Text: event.Text, PayloadJson: event.Payload, TimestampUnixMs: event.CreatedAt.UnixMilli(),
		})
	}
	return detail, nil
}

func (s *orchestratorServer) GetAgentDiff(ctx context.Context, req *pb.AgentDiffRequest) (*pb.AgentDiffResponse, error) {
	if s.codeTools == nil {
		return &pb.AgentDiffResponse{Error: "beemo-code is unavailable"}, nil
	}
	workspace := strings.TrimSpace(req.GetWorkspace())
	if s.agentStore != nil && strings.TrimSpace(req.GetSessionId()) != "" {
		session, _, err := s.agentStore.GetSession(ctx, s.cfg.MemoryUserKey, req.GetSessionId())
		if err != nil && workspace == "" {
			return nil, err
		}
		if err == nil {
			workspace = session.Workspace
		}
	}
	if workspace == "" {
		return &pb.AgentDiffResponse{Error: "no workspace is selected"}, nil
	}
	result, err := s.codeTools.Execute(ctx, &pb.CodeToolRequest{
		SessionId: req.GetSessionId(), Workspace: workspace, Action: "code.git_diff", ArgsJson: `{}`,
	})
	if err != nil {
		return nil, err
	}
	if result.GetStatus() != "ok" {
		return &pb.AgentDiffResponse{Error: result.GetError()}, nil
	}
	return &pb.AgentDiffResponse{Diff: result.GetOutput()}, nil
}

func (s *orchestratorServer) sendAgentEvent(ctx context.Context, stream pb.Orchestrator_RunAgentServer, sessionID, eventType, tool, text string, payload any, approvalID string) error {
	payloadJSON := ""
	if payload != nil {
		if encoded, err := json.Marshal(payload); err == nil {
			payloadJSON = string(encoded)
		}
	}
	event := &pb.AgentEvent{
		SessionId: sessionID, Type: eventType, Tool: tool, Text: text,
		PayloadJson: payloadJSON, TimestampUnixMs: time.Now().UnixMilli(), ApprovalId: approvalID,
	}
	if s.agentStore != nil {
		if err := s.agentStore.AppendEvent(ctx, sessionID, eventType, tool, text, payload); err != nil {
			s.log().Warn("orch.agent.event_store", "session", sessionID, "type", eventType, "err", err)
		}
		if eventType == "complete" || eventType == "error" {
			if err := s.agentStore.UpdateSessionStatus(ctx, sessionID, eventType); err != nil {
				s.log().Warn("orch.agent.session_status", "session", sessionID, "status", eventType, "err", err)
			}
		}
	}
	return stream.Send(event)
}

func (s *orchestratorServer) saveAgentSession(ctx context.Context, req *pb.AgentRequest, mode, status string) error {
	if s.agentStore == nil {
		return nil
	}
	return s.agentStore.UpsertSession(ctx, req.GetSessionId(), s.cfg.MemoryUserKey, mode, req.GetWorkspace(), status)
}

func buildCodeAgentPrompt(req *pb.AgentRequest, observations []agentObservation, step, maxSteps int) string {
	messages, _ := json.Marshal(req.GetMessages())
	results, _ := json.Marshal(observations)
	return fmt.Sprintf(`You are Beemo in explicit Code mode. Work carefully in the selected repository.
You may use Memory Palace and ordinary Beemo tools when relevant. Inspect before editing, preserve unrelated changes, run focused verification, and inspect the final diff.
Return exactly one JSON object, without markdown:
{"type":"tool","tool":"code.search","args":{"query":"name","path":"."}}
or {"type":"final","text":"concise result and verification"}.

Available tools:
- code.list {path}
- code.read {path, offset?, limit?}
- code.search {query, path?}
- code.patch {patch} using a standard unified Git diff
- code.exec {command}
- code.process_start {command}
- code.process_poll {process_id}
- code.process_input {process_id, input}
- code.process_stop {process_id}
- code.git_status {}
- code.git_diff {}
- memory.search {query, limit?}
- memory.remember {title, text}
- get_time {}
- weather {location, when?}
- calculator {operation and operation-specific values}
- older_sister {query}

Do not claim success until verification supports it. Dependency downloads, destructive commands, commits, pushes, and network commands require user approval.
Workspace: %s
Step: %d of %d
Conversation JSON: %s
Tool observations JSON: %s
Next decision:`, req.GetWorkspace(), step, maxSteps, messages, results)
}

func parseAgentDecision(text string) (agentDecision, error) {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end < start {
		return agentDecision{}, fmt.Errorf("response did not contain a JSON object")
	}
	var decision agentDecision
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), &decision); err != nil {
		return agentDecision{}, err
	}
	decision.Type = strings.ToLower(strings.TrimSpace(decision.Type))
	decision.Tool = strings.TrimSpace(decision.Tool)
	return decision, nil
}

func appendBoundedObservations(values []agentObservation, value agentObservation, limit int) []agentObservation {
	values = append(values, value)
	if len(values) <= limit {
		return values
	}
	return append([]agentObservation(nil), values[len(values)-limit:]...)
}
