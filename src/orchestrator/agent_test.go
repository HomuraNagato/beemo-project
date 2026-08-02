package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type fakeCodeToolsClient struct {
	actions        []string
	changedActions map[string]bool
}

func (f *fakeCodeToolsClient) Health(context.Context, *pb.CodeHealthRequest, ...grpc.CallOption) (*pb.CodeHealthResponse, error) {
	return &pb.CodeHealthResponse{Ready: true}, nil
}

func (f *fakeCodeToolsClient) ListWorkspaces(context.Context, *pb.ListWorkspacesRequest, ...grpc.CallOption) (*pb.ListWorkspacesResponse, error) {
	return &pb.ListWorkspacesResponse{Roots: []string{"/repo"}}, nil
}

func (f *fakeCodeToolsClient) Execute(_ context.Context, req *pb.CodeToolRequest, _ ...grpc.CallOption) (*pb.CodeToolResult, error) {
	f.actions = append(f.actions, req.GetAction())
	if req.GetAction() == "code.read" {
		return &pb.CodeToolResult{Action: req.GetAction(), Status: "error", Error: "not found"}, nil
	}
	return &pb.CodeToolResult{Action: req.GetAction(), Status: "ok", Output: "ok", Changed: f.changedActions[req.GetAction()]}, nil
}

type recordingAgentStream struct {
	ctx    context.Context
	events []*pb.AgentEvent
}

func (s *recordingAgentStream) Send(event *pb.AgentEvent) error {
	s.events = append(s.events, event)
	return nil
}

func (s *recordingAgentStream) SetHeader(metadata.MD) error  { return nil }
func (s *recordingAgentStream) SendHeader(metadata.MD) error { return nil }
func (s *recordingAgentStream) SetTrailer(metadata.MD)       {}
func (s *recordingAgentStream) Context() context.Context     { return s.ctx }
func (s *recordingAgentStream) SendMsg(any) error            { return nil }
func (s *recordingAgentStream) RecvMsg(any) error            { return io.EOF }

func TestRunAgentCodeModeIteratesToolThenFinal(t *testing.T) {
	codeTools := &fakeCodeToolsClient{}
	responses := []string{
		`{"type":"tool","tool":"code.search","args":{"query":"needle","path":"."}}`,
		`{"type":"final","text":"Found and verified the relevant code."}`,
	}
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL: "http://reasoning", LLMModel: "test", LLMTimeoutMs: 1000,
			CodeMaxSteps: 4, CodeMaxTokens: 128, CodeApprovalTimeoutMs: 1000,
		},
		codeTools: codeTools,
		callAgentCompletion: func(context.Context, string, string, string, int, time.Duration) (string, error) {
			response := responses[0]
			responses = responses[1:]
			return response, nil
		},
	}
	stream := &recordingAgentStream{ctx: context.Background()}
	err := server.RunAgent(&pb.AgentRequest{
		SessionId: "code-test", Mode: "code", Workspace: "/repo",
		Messages: []*pb.ChatMessage{{Role: "user", Content: "find the implementation"}},
	}, stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 0 {
		t.Fatalf("expected both model decisions to be consumed")
	}
	if !containsString(codeTools.actions, "code.search") {
		t.Fatalf("expected code.search, actions=%v", codeTools.actions)
	}
	if len(stream.events) == 0 || stream.events[len(stream.events)-1].GetType() != "complete" {
		t.Fatalf("expected complete event, got %#v", stream.events)
	}
}

func TestParseAgentDecisionAcceptsFencedJSON(t *testing.T) {
	decision, err := parseAgentDecision("```json\n{\"type\":\"final\",\"text\":\"done\"}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Type != "final" || decision.Text != "done" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestParseAgentDecisionUsesFirstCompleteObject(t *testing.T) {
	decision, err := parseAgentDecision(
		`{"type":"tool","tool":"code.create","args":{"path":"main.go","content":"package main"}}` +
			`{"type":"tool","tool":"code.exec","args":{"command":"go run main.go"}}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Type != "tool" || decision.Tool != "code.create" {
		t.Fatalf("expected first complete decision, got %#v", decision)
	}
}

func TestRunAgentSkipsConsecutiveDuplicateToolCall(t *testing.T) {
	codeTools := &fakeCodeToolsClient{}
	responses := []string{
		`{"type":"tool","tool":"code.search","args":{"query":"retrieval","path":"src"}}`,
		`{"type":"tool","tool":"code.search","args":{"query":"retrieval","path":"src"}}`,
		`{"type":"final","text":"done"}`,
	}
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL: "http://reasoning", LLMModel: "test", LLMTimeoutMs: 1000,
			CodeMaxSteps: 4, CodeMaxTokens: 128, CodeMaxPromptChars: 12000,
		},
		codeTools: codeTools,
		callAgentCompletion: func(context.Context, string, string, string, int, time.Duration) (string, error) {
			response := responses[0]
			responses = responses[1:]
			return response, nil
		},
	}
	stream := &recordingAgentStream{ctx: context.Background()}
	if err := server.RunAgent(&pb.AgentRequest{
		SessionId: "duplicate-test", Mode: "code", Workspace: "/repo",
		Messages: []*pb.ChatMessage{{Role: "user", Content: "find retrieval"}},
	}, stream); err != nil {
		t.Fatal(err)
	}
	searches := 0
	for _, action := range codeTools.actions {
		if action == "code.search" {
			searches++
		}
	}
	if searches != 1 {
		t.Fatalf("expected one executed search, got %d actions=%v", searches, codeTools.actions)
	}
}

func TestRunAgentSummarizesAtToolLimit(t *testing.T) {
	responses := []string{
		`{"type":"tool","tool":"code.search","args":{"query":"retrieval","path":"src"}}`,
		"I inspected the retrieval code but did not finish the requested change. The task is incomplete because the tool budget was reached.",
	}
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL: "http://reasoning", LLMModel: "test", LLMTimeoutMs: 1000,
			CodeMaxSteps: 1, CodeMaxTokens: 128, CodeMaxPromptChars: 12000,
		},
		codeTools: &fakeCodeToolsClient{},
		callAgentCompletion: func(context.Context, string, string, string, int, time.Duration) (string, error) {
			response := responses[0]
			responses = responses[1:]
			return response, nil
		},
	}
	stream := &recordingAgentStream{ctx: context.Background()}
	if err := server.RunAgent(&pb.AgentRequest{
		SessionId: "limit-test", Mode: "code", Workspace: "/repo",
		Messages: []*pb.ChatMessage{{Role: "user", Content: "inspect retrieval"}},
	}, stream); err != nil {
		t.Fatal(err)
	}
	if len(responses) != 0 {
		t.Fatalf("expected limit summary call")
	}
	last := stream.events[len(stream.events)-1]
	if last.GetType() != "complete" || last.GetText() != "tool_limit" {
		t.Fatalf("expected normal tool-limit completion, got %#v", last)
	}
	for _, event := range stream.events {
		if event.GetType() == "error" {
			t.Fatalf("tool limit should not emit an error: %#v", event)
		}
	}
}

func TestRunAgentRequiresVerificationAfterFileChange(t *testing.T) {
	responses := []string{
		`{"type":"tool","tool":"code.create","args":{"path":"main.go","content":"package main"}}`,
		`{"type":"final","text":"done without verification"}`,
		`{"type":"tool","tool":"code.exec","args":{"command":"go test ./..."}}`,
		`{"type":"final","text":"created and verified"}`,
	}
	codeTools := &fakeCodeToolsClient{changedActions: map[string]bool{"code.create": true}}
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL: "http://reasoning", LLMModel: "test", LLMTimeoutMs: 1000,
			CodeMaxSteps: 4, CodeMaxTokens: 128, CodeMaxPromptChars: 12000,
		},
		codeTools: codeTools,
		callAgentCompletion: func(context.Context, string, string, string, int, time.Duration) (string, error) {
			response := responses[0]
			responses = responses[1:]
			return response, nil
		},
	}
	stream := &recordingAgentStream{ctx: context.Background()}
	if err := server.RunAgent(&pb.AgentRequest{
		SessionId: "verification-test", Mode: "code", Workspace: "/repo",
		Messages: []*pb.ChatMessage{{Role: "user", Content: "create main.go"}},
	}, stream); err != nil {
		t.Fatal(err)
	}
	if len(responses) != 0 || !containsString(codeTools.actions, "code.exec") {
		t.Fatalf("expected verification before final response, remaining=%d actions=%v", len(responses), codeTools.actions)
	}
	for _, event := range stream.events {
		if event.GetType() == "assistant" && event.GetText() == "done without verification" {
			t.Fatal("accepted final response before verification")
		}
	}
}

func TestCodeAgentPromptUsesTargetedSearchAndStaysBounded(t *testing.T) {
	req := &pb.AgentRequest{
		Workspace: "/repo",
		Messages: []*pb.ChatMessage{
			{Role: "user", Content: strings.Repeat("conversation ", 2000)},
			{Role: "user", Content: "find the retrieval path"},
		},
	}
	observations := []agentObservation{
		{Tool: "code.search", Output: strings.Repeat("large search result\n", 4000)},
		{Tool: "code.read", Output: "latest relevant result"},
	}
	prompt := buildCodeAgentPrompt(req, observations, 2, 24, 12000)
	if len(prompt) > 12000 {
		t.Fatalf("prompt exceeded context budget: %d", len(prompt))
	}
	for _, expected := range []string{"code.files", "fixed_strings", "find the retrieval path", "latest relevant result"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q", expected)
		}
	}
	if strings.Contains(prompt, `"query":"name"`) {
		t.Fatal("prompt retained the generic search example")
	}
	if strings.Contains(prompt, `"query":"RunAgent"`) {
		t.Fatal("prompt retained a concrete search example")
	}
}
