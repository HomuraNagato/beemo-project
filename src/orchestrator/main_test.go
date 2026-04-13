package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/config"
	orchtools "eve-beemo/src/orchestrator/tools"
)

func TestChatFinalResponseFlow(t *testing.T) {
	t.Parallel()

	var finalPrompt string
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			if httpURL != "http://llm.test/v1/chat/completions" {
				t.Fatalf("unexpected chat URL for tool decision: %s", httpURL)
			}
			if model != "test-model" {
				t.Fatalf("unexpected model: %s", model)
			}
			if !strings.Contains(prompt, "what is 20% of 85?") {
				t.Fatalf("decision prompt missing user query: %q", prompt)
			}
			if grammar == "" {
				t.Fatal("expected grammar to be provided")
			}
			return `[{"tool":"calculator","args":{"operation":"percent_of","percent":20,"value":85}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalPrompt = prompt
			if httpURL != "http://llm.test/v1/chat/completions" {
				t.Fatalf("unexpected chat URL: %s", httpURL)
			}
			if model != "test-model" {
				t.Fatalf("unexpected model: %s", model)
			}
			if !strings.Contains(prompt, `Tool result: tool=calculator result=20% of 85 = 17`) {
				t.Fatalf("final prompt missing tool result: %q", prompt)
			}
			if !strings.Contains(prompt, "Original user query: what is 20% of 85?") {
				t.Fatalf("final prompt missing original user query: %q", prompt)
			}
			if !strings.Contains(prompt, "Latest user reply: what is 20% of 85?") {
				t.Fatalf("final prompt missing latest user reply: %q", prompt)
			}
			return "20% of 85 is 17.", nil
		},
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is 20% of 85?"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.GetText() != "20% of 85 is 17." {
		t.Fatalf("unexpected response text: %q", resp.GetText())
	}
	if finalPrompt == "" {
		t.Fatal("expected final response prompt to be generated")
	}
}

func TestChatReturnsNeedsInputWithoutFinalLLMCall(t *testing.T) {
	t.Parallel()

	finalCalled := false
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return `[{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"kg","value":45}]}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalled = true
			return "should not be called", nil
		},
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

func TestChatStripsHallucinatedBMIWeightFromFreshDecision(t *testing.T) {
	t.Parallel()

	finalCalled := false
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return `[{"tool":"calculator","args":{"operation":"bmi","height":[{"unit":"in","value":64}],"weight":[{"unit":"lb","value":101}]}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalled = true
			return "should not be called", nil
		},
	}

	resp, err := server.Chat(context.Background(), chatRequest(`what is the bmi of 64"?`))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if finalCalled {
		t.Fatal("final LLM call should not run when hallucinated weight is stripped")
	}
	if got, want := resp.GetText(), "What is the weight?"; got != want {
		t.Fatalf("unexpected clarification: got %q want %q", got, want)
	}
}

func TestChatDeterministicallyFillsPendingWeightFromReply(t *testing.T) {
	t.Parallel()

	resumeCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			if strings.Contains(prompt, "Resume the pending tool call.") {
				resumeCalls++
				t.Fatalf("resume LLM call should not run for a deterministic weight reply")
			}
			return `[{"tool":"calculator","args":{"operation":"bmi","height":[{"unit":"in","value":64}]}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Tool result: tool=calculator result=BMI 16.27") {
				t.Fatalf("final prompt missing bmi result: %q", prompt)
			}
			if !strings.Contains(prompt, "Latest user reply: 43kg,") {
				t.Fatalf("final prompt missing latest reply: %q", prompt)
			}
			return "The BMI is 16.27.", nil
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest(`what is the bmi of 64"?`))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "What is the weight?"; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequestWithSession("test-session", "43kg,"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "The BMI is 16.27."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}
	if resumeCalls != 0 {
		t.Fatalf("unexpected resume LLM calls: %d", resumeCalls)
	}
}

func TestChatRetriesEmptyDecisionAndFillsBothFieldsFromReply(t *testing.T) {
	t.Parallel()

	decisionCalls := 0
	resumeCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			if strings.Contains(prompt, "Resume the pending tool call.") {
				resumeCalls++
				t.Fatalf("resume LLM should not be called when both values are parseable")
			}
			decisionCalls++
			if strings.Contains(prompt, "Previous answer: []") {
				return `[{"tool":"calculator","args":{"operation":"bmi"}}]`, nil
			}
			return `[]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Tool result: tool=calculator result=BMI 16.27") {
				t.Fatalf("final prompt missing bmi result: %q", prompt)
			}
			return "The BMI is 16.27.", nil
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("what is the bmi of 60?"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "What are the weight and height?"; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequestWithSession("test-session", "43kg and 64 inches"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "The BMI is 16.27."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}
	if decisionCalls != 2 {
		t.Fatalf("expected two decision calls, got %d", decisionCalls)
	}
	if resumeCalls != 0 {
		t.Fatalf("unexpected resume calls: %d", resumeCalls)
	}
}

func TestChatRetriesEmptyDecisionForDateQuery(t *testing.T) {
	t.Parallel()

	decisionCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			decisionCalls++
			if strings.Contains(prompt, "Previous answer: []") {
				return `[{"tool":"get_time","args":{}}]`, nil
			}
			return `[]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Original user query: what date will it be 5 days from today?") {
				t.Fatalf("final prompt missing original query: %q", prompt)
			}
			if !strings.Contains(prompt, "Tool result: tool=get_time result=") {
				t.Fatalf("final prompt missing get_time result: %q", prompt)
			}
			return "It will be five days after the reported current date.", nil
		},
	}

	resp, err := server.Chat(context.Background(), chatRequest("what date will it be 5 days from today?"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "It will be five days after the reported current date."; got != want {
		t.Fatalf("unexpected response: got %q want %q", got, want)
	}
	if decisionCalls != 2 {
		t.Fatalf("expected two decision calls, got %d", decisionCalls)
	}
}

func TestChatIncludesActiveThreadForFollowUpDateQuestion(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Active conversation thread:\nuser: what is today's date?\nassistant: It is April 8, 2026.\nuser: what about tomorrow?") {
				t.Fatalf("decision prompt missing active thread: %q", prompt)
			}
			return `[{"tool":"get_time","args":{}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Active conversation thread:\nuser: what is today's date?\nassistant: It is April 8, 2026.\nuser: what about tomorrow?") {
				t.Fatalf("final prompt missing active thread: %q", prompt)
			}
			if !strings.Contains(prompt, "Tool result: tool=get_time result=") {
				t.Fatalf("final prompt missing get_time result: %q", prompt)
			}
			return "Tomorrow is one day after the reported current date.", nil
		},
	}

	resp, err := server.Chat(context.Background(), &pb.ChatRequest{
		SessionId: "test-session",
		Messages: []*pb.ChatMessage{
			{Role: "user", Content: "what is today's date?"},
			{Role: "assistant", Content: "It is April 8, 2026."},
			{Role: "user", Content: "what about tomorrow?"},
		},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "Tomorrow is one day after the reported current date."; got != want {
		t.Fatalf("unexpected response: got %q want %q", got, want)
	}
}

func TestChatGroundsCalculatorUsingActiveThread(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Active conversation thread:\nuser: what is the bmi of 45kg and 64 inches?\nassistant: The BMI is 17.03.\nuser: what about bmr for a 34 year old female?") {
				t.Fatalf("decision prompt missing active thread: %q", prompt)
			}
			return `[{"tool":"calculator","args":{"operation":"bmr","age_years":34,"gender":"female","weight":[{"unit":"kg","value":45}],"height":[{"unit":"in","value":64}]}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Tool result: tool=calculator result=BMR 1135.00 kcal/day") {
				t.Fatalf("final prompt missing grounded bmr result: %q", prompt)
			}
			return "The BMR is 1135.00 kcal/day.", nil
		},
	}

	resp, err := server.Chat(context.Background(), &pb.ChatRequest{
		SessionId: "test-session",
		Messages: []*pb.ChatMessage{
			{Role: "user", Content: "what is the bmi of 45kg and 64 inches?"},
			{Role: "assistant", Content: "The BMI is 17.03."},
			{Role: "user", Content: "what about bmr for a 34 year old female?"},
		},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "The BMR is 1135.00 kcal/day."; got != want {
		t.Fatalf("unexpected response: got %q want %q", got, want)
	}
}

func TestChatUsesSelectedActiveThreadForConflictingMeasurements(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			if strings.Contains(prompt, "45kg") || strings.Contains(prompt, "64inches") {
				t.Fatalf("decision prompt kept stale measurement thread: %q", prompt)
			}
			if !strings.Contains(prompt, "Active conversation thread:\nuser: what is the bmi of 134lbs and 172cm?\nassistant: The BMI is 20.55.\nuser: what is the bmr?\nassistant: What are the age in years and gender?\nuser: 27 years old and female") {
				t.Fatalf("decision prompt missing selected active thread: %q", prompt)
			}
			return `[{"tool":"calculator","args":{"operation":"bmr","age_years":27,"gender":"female","weight":[{"unit":"lb","value":134}],"height":[{"unit":"cm","value":172}]}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if strings.Contains(prompt, "45kg") || strings.Contains(prompt, "64inches") {
				t.Fatalf("final prompt kept stale measurement thread: %q", prompt)
			}
			if !strings.Contains(prompt, "Active conversation thread:\nuser: what is the bmi of 134lbs and 172cm?\nassistant: The BMI is 20.55.\nuser: what is the bmr?\nassistant: What are the age in years and gender?\nuser: 27 years old and female") {
				t.Fatalf("final prompt missing selected active thread: %q", prompt)
			}
			if !strings.Contains(prompt, "Tool result: tool=calculator result=BMR 1386.81 kcal/day") {
				t.Fatalf("final prompt missing bmr result: %q", prompt)
			}
			return "The BMR is 1386.81 kcal/day.", nil
		},
	}

	resp, err := server.Chat(context.Background(), &pb.ChatRequest{
		SessionId: "test-session",
		Messages: []*pb.ChatMessage{
			{Role: "user", Content: "what is the bmi of 45kg?"},
			{Role: "assistant", Content: "What is the height?"},
			{Role: "user", Content: "64inches"},
			{Role: "assistant", Content: "64 inches, BMI 17.03"},
			{Role: "user", Content: "what is the bmi of 134lbs and 172cm?"},
			{Role: "assistant", Content: "The BMI is 20.55."},
			{Role: "user", Content: "what is the bmr?"},
			{Role: "assistant", Content: "What are the age in years and gender?"},
			{Role: "user", Content: "27 years old and female"},
		},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "The BMR is 1386.81 kcal/day."; got != want {
		t.Fatalf("unexpected response: got %q want %q", got, want)
	}
}

func TestChatResumesPendingToolState(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			switch {
			case strings.Contains(prompt, "Resume the pending tool call."):
				if !strings.Contains(prompt, "Original user query: what is the BMI of 45kg?") {
					t.Fatalf("resume prompt missing original query: %q", prompt)
				}
				if !strings.Contains(prompt, "Missing fields: height") {
					t.Fatalf("resume prompt missing missing-fields context: %q", prompt)
				}
				if !strings.Contains(prompt, "Latest user reply: 64 inches") {
					t.Fatalf("resume prompt missing latest reply: %q", prompt)
				}
				return `[{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"kg","value":45}],"height":[{"unit":"in","value":64}]}}]`, nil
			default:
				return `[{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"kg","value":45}]}}]`, nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Tool result: tool=calculator result=BMI 17.03") {
				t.Fatalf("final prompt missing resumed tool result: %q", prompt)
			}
			if !strings.Contains(prompt, "Original user query: what is the BMI of 45kg?") {
				t.Fatalf("final prompt missing original query: %q", prompt)
			}
			if !strings.Contains(prompt, "Latest user reply: 64 inches") {
				t.Fatalf("final prompt missing latest reply: %q", prompt)
			}
			return "The BMI is 17.03.", nil
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("what is the BMI of 45kg?"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "What is the height?"; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}
	if _, ok := server.getPending("test-session"); !ok {
		t.Fatal("expected pending state after clarification request")
	}

	secondResp, err := server.Chat(context.Background(), chatRequestWithSession("test-session", "64 inches"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "The BMI is 17.03."; got != want {
		t.Fatalf("unexpected resumed response: got %q want %q", got, want)
	}
	if _, ok := server.getPending("test-session"); ok {
		t.Fatal("expected pending state to be cleared after successful resume")
	}
}

func TestChatResumesPendingToolStateWhenModelSwitchesToConvert(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			switch {
			case strings.Contains(prompt, "Resume the pending tool call."):
				return `[{"tool":"calculator","args":{"operation":"convert","input":[{"unit":"kg","value":45}],"to_unit":"lb"}}]`, nil
			default:
				return `[{"tool":"calculator","args":{"operation":"bmi","height":[{"unit":"in","value":64}]}}]`, nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Tool result: tool=calculator result=BMI 17.03") {
				t.Fatalf("final prompt missing resumed bmi result: %q", prompt)
			}
			if !strings.Contains(prompt, "Original user query: what is the bmi of 64\"?") {
				t.Fatalf("final prompt missing original query: %q", prompt)
			}
			if !strings.Contains(prompt, "Latest user reply: 45kg") {
				t.Fatalf("final prompt missing latest reply: %q", prompt)
			}
			return "The BMI is 17.03.", nil
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("what is the bmi of 64\"?"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "What is the weight?"; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequestWithSession("test-session", "45kg"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "The BMI is 17.03."; got != want {
		t.Fatalf("unexpected resumed response: got %q want %q", got, want)
	}
}

func TestChatKeepsPendingQuestionWhenResumeDoesNotReturnUsableCall(t *testing.T) {
	t.Parallel()

	freshCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			if strings.Contains(prompt, "Resume the pending tool call.") {
				return `[]`, nil
			}
			freshCalls++
			return `[{"tool":"calculator","args":{"operation":"bmi","height":[{"unit":"in","value":64}]}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			t.Fatalf("final LLM should not be called")
			return "", nil
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("what is the bmi of 64\"?"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "What is the weight?"; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequestWithSession("test-session", "hmm"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "What is the weight?"; got != want {
		t.Fatalf("unexpected resumed response: got %q want %q", got, want)
	}
	if freshCalls != 1 {
		t.Fatalf("expected only the initial fresh decision call, got %d", freshCalls)
	}
}

func TestChatPreservesCollectedFieldsAcrossMultipleTDEEClarifications(t *testing.T) {
	t.Parallel()

	resumeCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			if strings.Contains(prompt, "Resume the pending tool call.") {
				resumeCalls++
				t.Fatalf("resume LLM should not be needed for deterministic height/activity replies")
			}
			return `[{"tool":"calculator","args":{"operation":"tdee","age_years":35,"gender":"female","weight":[{"unit":"kg","value":42}]}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Tool result: tool=calculator result=TDEE 1512.50 kcal/day") {
				t.Fatalf("final prompt missing tdee result: %q", prompt)
			}
			if !strings.Contains(prompt, "Original user query: what is the tdee of a 35 year old female with weight 42kg?") {
				t.Fatalf("final prompt missing original tdee query: %q", prompt)
			}
			if !strings.Contains(prompt, "Latest user reply: light") {
				t.Fatalf("final prompt missing latest reply: %q", prompt)
			}
			return "The TDEE is 1512.50 kcal/day.", nil
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("what is the tdee of a 35 year old female with weight 42kg?"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "What is the height?"; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequestWithSession("test-session", `5'4"`))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "What is the activity level: sedentary, light, moderate, active, or very_active?"; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}

	thirdResp, err := server.Chat(context.Background(), chatRequestWithSession("test-session", "light"))
	if err != nil {
		t.Fatalf("third Chat returned error: %v", err)
	}
	if got, want := thirdResp.GetText(), "The TDEE is 1512.50 kcal/day."; got != want {
		t.Fatalf("unexpected third response: got %q want %q", got, want)
	}

	if _, ok := server.getPending("test-session"); ok {
		t.Fatal("expected pending state to be cleared after successful TDEE completion")
	}
	if resumeCalls != 0 {
		t.Fatalf("unexpected resume calls: %d", resumeCalls)
	}
}

func TestChatCarriesAgeIntoTDEEFollowUpFromActiveThread(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Active conversation thread:\nuser: what is the bmr of 45kg?\nassistant: What are the age in years, gender, and height?\nuser: 34 years, female, 162cm\nassistant: The BMR for a 34-year-old female weighing 45kg, who is 162cm tall, is 1131.50 kcal/day.\nuser: what is the tdee?") {
				t.Fatalf("decision prompt missing tdee follow-up thread: %q", prompt)
			}
			return `[{"tool":"calculator","args":{"operation":"tdee","age_years":34,"gender":"female","weight":[{"unit":"kg","value":45}],"height":[{"unit":"cm","value":162}]}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			t.Fatalf("final LLM should not be called for missing activity level")
			return "", nil
		},
	}

	resp, err := server.Chat(context.Background(), &pb.ChatRequest{
		SessionId: "test-session",
		Messages: []*pb.ChatMessage{
			{Role: "user", Content: "what is the bmr of 45kg?"},
			{Role: "assistant", Content: "What are the age in years, gender, and height?"},
			{Role: "user", Content: "34 years, female, 162cm"},
			{Role: "assistant", Content: "The BMR for a 34-year-old female weighing 45kg, who is 162cm tall, is 1131.50 kcal/day."},
			{Role: "user", Content: "what is the tdee?"},
		},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "What is the activity level: sedentary, light, moderate, active, or very_active?"; got != want {
		t.Fatalf("unexpected response: got %q want %q", got, want)
	}
}

func TestChatReturnsParseErrorOnInvalidDecisionJSON(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return `[{"tool":"calculator"`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			return "unexpected", nil
		},
	}

	_, err := server.Chat(context.Background(), chatRequest("what is 20% of 85?"))
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestChatStoresPendingOriginFromCurrentRequestNotOldTranscript(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return `[{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"kg","value":45}]}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			t.Fatalf("final LLM should not be called")
			return "", nil
		},
	}

	_, err := server.Chat(context.Background(), &pb.ChatRequest{
		SessionId: "test-session",
		Messages: []*pb.ChatMessage{
			{Role: "user", Content: "what time is it?"},
			{Role: "assistant", Content: "It is noon."},
			{Role: "user", Content: "what is the BMI of 45kg?"},
		},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}

	pending, ok := server.getPending("test-session")
	if !ok {
		t.Fatal("expected pending state")
	}
	if got, want := pending.OriginalUserQuery, "what is the BMI of 45kg?"; got != want {
		t.Fatalf("unexpected pending original query: got %q want %q", got, want)
	}
}

func chatRequest(userQuery string) *pb.ChatRequest {
	return chatRequestWithSession("test-session", userQuery)
}

func chatRequestWithSession(sessionID, userQuery string) *pb.ChatRequest {
	return &pb.ChatRequest{
		SessionId: sessionID,
		Messages: []*pb.ChatMessage{
			{Role: "user", Content: userQuery},
		},
	}
}

func TestParseToolCallsDefaultsEmptyArgs(t *testing.T) {
	t.Parallel()

	calls, err := parseToolCalls(`[{"tool":"get_time"}]`)
	if err != nil {
		t.Fatalf("parseToolCalls returned error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("unexpected call count: %d", len(calls))
	}
	if got, want := string(calls[0].Args), "{}"; got != want {
		t.Fatalf("unexpected args default: got %s want %s", got, want)
	}
}

func TestParseToolCallsRejectsMissingTool(t *testing.T) {
	t.Parallel()

	_, err := parseToolCalls(`[{"tool":"   ","args":{}}]`)
	if err == nil {
		t.Fatal("expected missing tool error, got nil")
	}
	if got, want := err.Error(), "tool call 0 missing tool name"; got != want {
		t.Fatalf("unexpected error: got %q want %q", got, want)
	}
}

func TestToolNames(t *testing.T) {
	t.Parallel()

	got := toolNames([]toolCall{{Tool: "calculator"}, {Tool: "get_time"}})
	want := []string{"calculator", "get_time"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("unexpected tool names: got %v want %v", got, want)
	}
}
