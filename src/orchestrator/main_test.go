package main

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/config"
	"eve-beemo/src/orchestrator/routing"
	orchtools "eve-beemo/src/orchestrator/tools"
)

type staticRouteSelector struct {
	candidates []routing.Candidate
	err        error
}

func (s staticRouteSelector) Retrieve(query string, timeout time.Duration) ([]routing.Candidate, error) {
	return s.candidates, s.err
}

func TestChatFinalResponseFlow(t *testing.T) {
	t.Parallel()

	var finalPrompt string
	server := testServer(t)
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		if !strings.Contains(prompt, "what is 20% of 85?") {
			t.Fatalf("decision prompt missing user query: %q", prompt)
		}
		return `[{"tool":"calculator","args":{"operation":"percent_of","percent":20,"value":85}}]`, nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		finalPrompt = prompt
		if !strings.Contains(prompt, `Tool result: tool=calculator result=20% of 85 = 17`) {
			t.Fatalf("final prompt missing tool result: %q", prompt)
		}
		return "20% of 85 is 17.", nil
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is 20% of 85?"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "20% of 85 is 17."; got != want {
		t.Fatalf("unexpected response text: got %q want %q", got, want)
	}
	if finalPrompt == "" {
		t.Fatal("expected final response prompt")
	}
}

func TestChatReturnsNeedsInputWithoutFinalLLMCall(t *testing.T) {
	t.Parallel()

	finalCalled := false
	server := testServer(t)
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		return `[{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"kg","value":45}]}}]`, nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		finalCalled = true
		return "should not be called", nil
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is the BMI of 45kg?"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if finalCalled {
		t.Fatal("final LLM call should not run for needs_input")
	}
	if got, want := resp.GetText(), "What is the height?"; got != want {
		t.Fatalf("unexpected clarification: got %q want %q", got, want)
	}
}

func TestChatUsesDeterministicTimeFastPath(t *testing.T) {
	t.Parallel()

	server := testServer(t)
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		t.Fatalf("decision LLM should not be called for simple time request: %q", prompt)
		return "", nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		t.Fatalf("final LLM should not be called for simple time request: %q", prompt)
		return "", nil
	}

	resp, err := server.Chat(context.Background(), chatRequest("what time is it?"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got := resp.GetText(); !strings.HasPrefix(got, "It is ") {
		t.Fatalf("unexpected response text: %q", got)
	}
}

func TestChatUsesRoutedPromptWhenCandidatesAvailable(t *testing.T) {
	t.Parallel()

	server := testServer(t)
	server.routeSelector = staticRouteSelector{
		candidates: []routing.Candidate{
			{
				Route: routing.Route{
					ID:      "calculator.percent_of",
					Title:   "Percent of a value",
					Summary: "Compute a percent of a value.",
					Handler: routing.Handler{Type: "tool", Target: "calculator"},
					DefaultArgs: map[string]any{
						"operation": "percent_of",
					},
					ExampleRequests: []string{"what is 20% of 85?"},
				},
				Score: 0.91,
			},
		},
	}
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		if !strings.Contains(prompt, "Candidate routes:") {
			t.Fatalf("expected routed prompt, got %q", prompt)
		}
		if !strings.Contains(prompt, "route_id: calculator.percent_of") {
			t.Fatalf("expected routed candidate id, got %q", prompt)
		}
		return `[{"tool":"calculator","args":{"operation":"percent_of","percent":20,"value":85}}]`, nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		return "20% of 85 is 17.", nil
	}

	resp, err := server.Chat(context.Background(), chatRequest("calculate 20 percent of 85"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "20% of 85 is 17."; got != want {
		t.Fatalf("unexpected response text: got %q want %q", got, want)
	}
}

func TestPendingFieldsSatisfiedRequiresAllMissingFields(t *testing.T) {
	t.Parallel()

	args := []byte(`{"height":[{"unit":"cm","value":162}]}`)
	if pendingFieldsSatisfied([]string{"height", "weight"}, args) {
		t.Fatal("expected pending fields to remain unsatisfied when only one field is present")
	}
	if !pendingFieldsSatisfied([]string{"height"}, args) {
		t.Fatal("expected pending field to be satisfied")
	}
}

func testServer(t *testing.T) *orchestratorServer {
	t.Helper()
	return &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:         "http://llm.test/v1/chat/completions",
			LLMModel:           "test-model",
			LLMTimeoutMs:       500,
			EmbeddingTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
	}
}

func chatRequest(text string) *pb.ChatRequest {
	return &pb.ChatRequest{
		SessionId: "test-session",
		Messages: []*pb.ChatMessage{
			{Role: "user", Content: text},
		},
	}
}
