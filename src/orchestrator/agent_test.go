package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/config"
	orchtools "eve-beemo/src/orchestrator/tools"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type fakeAgentTools struct {
	requests []orchtools.Request
	results  []orchtools.Result
}

func (f *fakeAgentTools) Execute(_ context.Context, req orchtools.Request) (orchtools.Result, error) {
	f.requests = append(f.requests, req)
	if len(f.results) == 0 {
		return orchtools.Result{Action: req.Action, Output: "ok"}, nil
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result, nil
}

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

func (f *fakeCodeToolsClient) BrowseDirectories(context.Context, *pb.BrowseDirectoriesRequest, ...grpc.CallOption) (*pb.BrowseDirectoriesResponse, error) {
	return &pb.BrowseDirectoriesResponse{Path: "/repo", IsGit: true, Roots: []string{"/repo"}}, nil
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

func TestRunAgentNormalUsesBoundedToolLoop(t *testing.T) {
	responses := []string{
		`{"type":"tool","tool":"calculator","args":{"operation":"expression","expression":"2+2"}}`,
		`{"type":"final","text":"Four."}`,
	}
	toolExecutor := &fakeAgentTools{results: []orchtools.Result{{Action: "calculator", Output: "4"}}}
	server := &orchestratorServer{
		cfg:   config.Config{LLMHTTPURL: "http://reasoning", LLMModel: "test", LLMTimeoutMs: 1000, NormalMaxRounds: 4, CodeMaxTokens: 128},
		tools: toolExecutor,
		callAgentCompletion: func(_ context.Context, _, _, prompt string, _ int, _ time.Duration) (string, error) {
			if !strings.Contains(prompt, "normal profile") {
				t.Fatalf("normal profile missing from prompt")
			}
			response := responses[0]
			responses = responses[1:]
			return response, nil
		},
	}
	stream := &recordingAgentStream{ctx: context.Background()}
	if err := server.RunAgent(&pb.AgentRequest{SessionId: "normal-test", Mode: "normal", Messages: []*pb.ChatMessage{{Role: "user", Content: "what is 2+2?"}}}, stream); err != nil {
		t.Fatal(err)
	}
	if len(toolExecutor.requests) != 1 || toolExecutor.requests[0].Action != "calculator" {
		t.Fatalf("unexpected tool requests: %#v", toolExecutor.requests)
	}
	if got := assistantEventText(stream.events); got != "Four." {
		t.Fatalf("unexpected answer %q", got)
	}
}

func TestConversationAgentGuardsRequiredInternetEvidence(t *testing.T) {
	responses := []string{
		`{"type":"final","text":"Try Imaginary Garden."}`,
		`{"tool":"internet.search","tool":"search_query":"restaurants near Elmhurst NY currently open"}`,
		`{"type":"final","text":"The current evidence supports Example Garden [1]."}`,
	}
	toolExecutor := &fakeAgentTools{results: []orchtools.Result{{
		Action: "internet.search", Output: "[1] Example Garden\nEvidence:\nRestaurant in Elmhurst.",
		Sources: []orchtools.Source{{Title: "Example Garden", URL: "https://example.test/garden"}},
	}}}
	server := &orchestratorServer{
		cfg:   config.Config{LLMHTTPURL: "http://reasoning", LLMModel: "test", LLMTimeoutMs: 1000, NormalMaxRounds: 4, CodeMaxTokens: 128},
		tools: toolExecutor,
		callAgentCompletion: func(_ context.Context, _, _, prompt string, _ int, _ time.Duration) (string, error) {
			if !strings.Contains(prompt, "Evidence requirement: required via internet.search") {
				t.Fatalf("required evidence policy missing from prompt: %s", prompt)
			}
			response := responses[0]
			responses = responses[1:]
			return response, nil
		},
	}
	stream := &recordingAgentStream{ctx: context.Background()}
	req := &pb.AgentRequest{
		SessionId: "evidence-test", Mode: "normal",
		Messages: []*pb.ChatMessage{{Role: "user", Content: "What's a good restaurant near Elmhurst NY?"}},
		Options:  map[string]string{"internet_enabled": "true"},
	}
	if err := server.runConversationAgent(req, stream, server.conversationProfile("normal")); err != nil {
		t.Fatal(err)
	}
	if len(responses) != 0 || len(toolExecutor.requests) != 1 {
		t.Fatalf("expected rejected final followed by recovered search and cited answer; responses=%d requests=%#v", len(responses), toolExecutor.requests)
	}
	var searchArgs struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(toolExecutor.requests[0].Args, &searchArgs); err != nil {
		t.Fatal(err)
	}
	if searchArgs.Query != "What's a good restaurant near Elmhurst NY?" {
		t.Fatalf("recovered search used unexpected query %q", searchArgs.Query)
	}
	if got := assistantEventText(stream.events); !strings.Contains(got, "Example Garden [1]") {
		t.Fatalf("unexpected grounded answer %q", got)
	}
	for _, event := range stream.events {
		if event.GetType() != "tool_start" {
			continue
		}
		var payload struct {
			Progress agentProgress   `json:"progress"`
			Args     json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal([]byte(event.GetPayloadJson()), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Progress.Step != 2 || !strings.Contains(string(payload.Args), "Elmhurst") {
			t.Fatalf("tool event omitted progress or args: %#v", payload)
		}
	}
}

func TestConversationAgentDoesNotExposeMalformedFinalDecision(t *testing.T) {
	server := &orchestratorServer{
		cfg: config.Config{LLMHTTPURL: "http://reasoning", LLMModel: "test", LLMTimeoutMs: 1000, NormalMaxRounds: 1, CodeMaxTokens: 128},
		callAgentCompletion: func(context.Context, string, string, string, int, time.Duration) (string, error) {
			return `{"tool":"internet.search","tool":"search_query":"current news"}`, nil
		},
	}
	stream := &recordingAgentStream{ctx: context.Background()}
	req := &pb.AgentRequest{SessionId: "malformed-final-test", Messages: []*pb.ChatMessage{{Role: "user", Content: "Tell me something timeless."}}}
	if err := server.runConversationAgent(req, stream, server.conversationProfile("normal")); err != nil {
		t.Fatal(err)
	}
	answer := assistantEventText(stream.events)
	if strings.Contains(answer, `"tool"`) || !strings.Contains(answer, "invalid response") {
		t.Fatalf("malformed decision was exposed to the user: %q", answer)
	}
}

func TestConversationAgentRequiresCitationAfterInternetSearch(t *testing.T) {
	responses := []string{
		`{"type":"tool","tool":"internet.search","args":{"query":"is Example Garden still open"}}`,
		`{"type":"final","text":"It is open."}`,
		`{"type":"final","text":"The source currently lists it as open [1]."}`,
	}
	server := &orchestratorServer{
		cfg: config.Config{LLMHTTPURL: "http://reasoning", LLMModel: "test", LLMTimeoutMs: 1000, NormalMaxRounds: 4, CodeMaxTokens: 128},
		tools: &fakeAgentTools{results: []orchtools.Result{{
			Action: "internet.search", Output: "[1] Example Garden\nEvidence:\nOpen today.",
			Sources: []orchtools.Source{{Title: "Example Garden", URL: "https://example.test/garden"}},
		}}},
		callAgentCompletion: func(context.Context, string, string, string, int, time.Duration) (string, error) {
			response := responses[0]
			responses = responses[1:]
			return response, nil
		},
	}
	stream := &recordingAgentStream{ctx: context.Background()}
	req := &pb.AgentRequest{
		SessionId: "citation-test", Messages: []*pb.ChatMessage{{Role: "user", Content: "Is Example Garden still open?"}},
		Options: map[string]string{"internet_enabled": "true"},
	}
	if err := server.runConversationAgent(req, stream, server.conversationProfile("normal")); err != nil {
		t.Fatal(err)
	}
	if len(responses) != 0 || !strings.Contains(assistantEventText(stream.events), "[1]") {
		t.Fatalf("expected uncited final to be rejected, responses=%d answer=%q", len(responses), assistantEventText(stream.events))
	}
}

func TestConversationAgentAcceptsCitedPlainTextOnFinalGroundedRound(t *testing.T) {
	responses := []string{
		`{"type":"final","text":"A current answer."}`,
		`{"tool":"internet.search","tool":"search_query":"current news"}`,
		`{"type":"final","text":"A summary without a citation."}`,
		`A concise current summary.`,
	}
	server := &orchestratorServer{
		cfg: config.Config{LLMHTTPURL: "http://reasoning", LLMModel: "test", LLMTimeoutMs: 1000, LLMContextTokens: 2048, NormalMaxRounds: 4, CodeMaxTokens: 1024},
		tools: &fakeAgentTools{results: []orchtools.Result{{
			Action: "internet.search", Output: "[1] Current report\nEvidence:\nVerified detail.",
			Sources: []orchtools.Source{{Title: "Current report", URL: "https://example.test/current"}},
		}}},
		callAgentCompletion: func(_ context.Context, _, _, prompt string, _ int, _ time.Duration) (string, error) {
			response := responses[0]
			responses = responses[1:]
			if len(responses) == 0 && !strings.Contains(prompt, "Return only the final answer text") {
				t.Fatalf("final grounded prompt retained the JSON envelope: %s", prompt)
			}
			return response, nil
		},
	}
	stream := &recordingAgentStream{ctx: context.Background()}
	req := &pb.AgentRequest{
		SessionId: "plain-final-test", Messages: []*pb.ChatMessage{{Role: "user", Content: "What is in the news right now?"}},
		Options: map[string]string{"internet_enabled": "true"},
	}
	if err := server.runConversationAgent(req, stream, server.conversationProfile("normal")); err != nil {
		t.Fatal(err)
	}
	if got := assistantEventText(stream.events); !strings.Contains(got, "concise current summary.") || !strings.Contains(got, "Sources:") {
		t.Fatalf("unexpected grounded plain-text answer %q", got)
	}
}

func TestConversationAgentExplainsWhenRequiredInternetIsDisabled(t *testing.T) {
	modelCalled := false
	server := &orchestratorServer{
		cfg: config.Config{NormalMaxRounds: 4, CodeMaxTokens: 128},
		callAgentCompletion: func(context.Context, string, string, string, int, time.Duration) (string, error) {
			modelCalled = true
			return `{"type":"final","text":"guessed"}`, nil
		},
	}
	stream := &recordingAgentStream{ctx: context.Background()}
	req := &pb.AgentRequest{SessionId: "offline-evidence-test", Messages: []*pb.ChatMessage{{Role: "user", Content: "Is that restaurant still open?"}}, Options: map[string]string{"internet_enabled": "false"}}
	if err := server.runConversationAgent(req, stream, server.conversationProfile("normal")); err != nil {
		t.Fatal(err)
	}
	if modelCalled || !strings.Contains(assistantEventText(stream.events), "/internet") {
		t.Fatalf("expected safe internet guidance, called=%t answer=%q", modelCalled, assistantEventText(stream.events))
	}
}

func TestRunAgentResearchIsTransient(t *testing.T) {
	var prompt string
	server := &orchestratorServer{
		cfg: config.Config{LLMHTTPURL: "http://reasoning", LLMModel: "test", LLMTimeoutMs: 1000, ResearchMaxRounds: 8, CodeMaxTokens: 128},
		callAgentCompletion: func(_ context.Context, _, _, value string, _ int, _ time.Duration) (string, error) {
			prompt = value
			return `{"type":"final","text":"Research complete."}`, nil
		},
	}
	stream := &recordingAgentStream{ctx: context.Background()}
	if err := server.RunAgent(&pb.AgentRequest{SessionId: "research-test", Mode: "normal", Messages: []*pb.ChatMessage{{Role: "user", Content: "/research retrieval systems"}}}, stream); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "research profile") || !strings.Contains(prompt, "retrieval systems") || strings.Contains(prompt, "/research retrieval systems") {
		t.Fatalf("unexpected research prompt: %s", prompt)
	}
	var progress agentProgress
	for _, event := range stream.events {
		if event.GetType() == "status" && event.GetText() == "Researching" {
			if err := json.Unmarshal([]byte(event.GetPayloadJson()), &progress); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if progress.MaxSteps != 8 {
		t.Fatalf("unexpected research limit: %#v", progress)
	}
}

func TestConversationPromptIncludesCoreContextAndInternetConditionally(t *testing.T) {
	req := &pb.AgentRequest{
		Messages: []*pb.ChatMessage{{Role: "user", Content: "find it"}},
		Options:  map[string]string{"core_context": "I prefer concise answers.", "internet_enabled": "true"},
	}
	prompt := buildConversationAgentPrompt(req, nil, conversationProfile{name: "normal", maxRounds: 4, maxTokens: 1024}, evidencePolicy{Level: evidencePreferred, Tool: "internet.search", Reason: "test"}, 1, 8192)
	for _, expected := range []string{"I prefer concise answers.", "internet.search", "Current local date and time", "Evidence requirement: preferred", "Prior assistant messages are conversational context, not proof", "Round: 1 of 4"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q", expected)
		}
	}
	if strings.Contains(prompt, "memory.remember") || strings.Contains(prompt, "get_time") || strings.Contains(prompt, "beemo.direct") {
		t.Fatalf("prompt exposed removed tools: %s", prompt)
	}
}

func assistantEventText(events []*pb.AgentEvent) string {
	for _, event := range events {
		if event.GetType() == "assistant" {
			return event.GetText()
		}
	}
	return ""
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

func TestStreamableFinalTextDecodesPartialJSON(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
		final    bool
	}{
		{name: "plain final", response: "A direct answer.", want: "A direct answer.", final: true},
		{name: "tool decision", response: `{"type":"tool","tool":"weather","args":{`, final: false},
		{name: "partial final", response: `{"type":"final","text":"First sentence. Sec`, want: "First sentence. Sec", final: true},
		{name: "escaped text", response: `{"type":"final","text":"Say \"hello\". Path C:\\temp`, want: `Say "hello". Path C:\temp`, final: true},
		{name: "incomplete escape", response: `{"type":"final","text":"Wait` + "\\", want: "Wait", final: true},
		{name: "markdown envelope", response: "```json\n{\"type\":\"final\",\"text\":\"hidden\"}", final: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, final := streamableFinalText(test.response)
			if got != test.want || final != test.final {
				t.Fatalf("streamableFinalText() = %q, %v; want %q, %v", got, final, test.want, test.final)
			}
		})
	}
}
