package main

import (
	"context"
	"io"
	"testing"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type fakeCodeToolsClient struct {
	actions []string
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
	return &pb.CodeToolResult{Action: req.GetAction(), Status: "ok", Output: "ok"}, nil
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
