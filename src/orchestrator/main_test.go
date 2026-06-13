package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"eve-beemo/src/orchestrator/config"
	"eve-beemo/src/orchestrator/factsel"
	"eve-beemo/src/orchestrator/memoryctx"
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

type staticFactSelector struct {
	attr  string
	err   error
	attrs []string
	facts map[string]factsel.Fact
}

func (s staticFactSelector) Select(query string, attrs []string, timeout time.Duration) (string, error) {
	return s.attr, s.err
}

func (s staticFactSelector) Attrs() []string {
	return append([]string(nil), s.attrs...)
}

func TestIntroducedSpeakerIDRejectsNumericAge(t *testing.T) {
	t.Parallel()

	if got := introducedSpeakerID("what is my bmr? I am 35 years old and female"); got != "" {
		t.Fatalf("age should not be treated as an introduced speaker, got %q", got)
	}
	if got, want := introducedSpeakerID("Hi BeeMo, I am Sabrina2"), "person:sabrina2"; got != want {
		t.Fatalf("unexpected alphanumeric speaker: got %q want %q", got, want)
	}
}

func TestChatDirectMemoryLookupUsesLatestCorrectedGenericFact(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			t.Fatalf("direct generic fact recall should not need decision LLM: %q", prompt)
			return "", nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			t.Fatalf("direct generic fact recall should not need final LLM: %q", prompt)
			return "", nil
		},
	}

	for _, text := range []string{
		"I am Serene",
		"my detail 042 is value-042",
		"that's wrong, my detail 042 is corrected-042",
	} {
		resp, err := server.Chat(context.Background(), chatRequest(text))
		if err != nil {
			t.Fatalf("Chat(%q) returned error: %v", text, err)
		}
		if got, want := resp.GetText(), "Got it."; got != want {
			t.Fatalf("unexpected response for %q: got %q want %q", text, got, want)
		}
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is my detail 042?"))
	if err != nil {
		t.Fatalf("recall Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "Your detail 042 is corrected-042."; got != want {
		t.Fatalf("unexpected corrected recall: got %q want %q", got, want)
	}
}

func TestChatGenericMemoryPhraseVariantsAvoidLLMFallback(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			t.Fatalf("generic phrase memory should not need decision LLM: %q", prompt)
			return "", nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			t.Fatalf("generic phrase memory should not need final LLM: %q", prompt)
			return "", nil
		},
	}

	steps := []struct {
		text string
		want string
	}{
		{text: "I am Serene", want: "Got it."},
		{text: "please remember my codename is Moonrise", want: "Got it."},
		{text: "can you remind me what my codename is?", want: "Your codename is Moonrise."},
		{text: "update my codename to Sunrise", want: "Got it."},
		{text: "what was my codename again?", want: "Your codename is Sunrise."},
		{text: "set my project motto to steady sparks", want: "Got it."},
		{text: "do you know my project motto?", want: "Your project motto is steady sparks."},
	}

	for _, step := range steps {
		step := step
		t.Run(step.text, func(t *testing.T) {
			resp, err := server.Chat(context.Background(), chatRequest(step.text))
			if err != nil {
				t.Fatalf("Chat returned error: %v", err)
			}
			if got := resp.GetText(); got != step.want {
				t.Fatalf("unexpected response: got %q want %q", got, step.want)
			}
		})
	}
}

func (s staticFactSelector) Fact(attr string) (factsel.Fact, bool) {
	fact, ok := s.facts[attr]
	return fact, ok
}

func (s staticFactSelector) QuestionPrompt(attrs []string) string {
	return "What fact should I look up?"
}

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

func TestChatUsesDeterministicTimeFastPath(t *testing.T) {
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
			t.Fatalf("decision LLM should not be called for simple time request: %q", prompt)
			return "", nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			t.Fatalf("final LLM should not be called for simple time request: %q", prompt)
			return "", nil
		},
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

	var sawRoutePrompt bool
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:         "http://llm.test/v1/chat/completions",
			LLMModel:           "test-model",
			LLMTimeoutMs:       500,
			EmbeddingTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutor(),
		routeSelector: staticRouteSelector{
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
		},
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Candidate routes:") {
				t.Fatalf("expected routed prompt, got %q", prompt)
			}
			if !strings.Contains(prompt, "route_id: calculator.percent_of") {
				t.Fatalf("expected routed candidate id, got %q", prompt)
			}
			sawRoutePrompt = true
			return `[{"tool":"calculator","args":{"operation":"percent_of","percent":20,"value":85}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			return "20% of 85 is 17.", nil
		},
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is 20% of 85?"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if !sawRoutePrompt {
		t.Fatal("expected routed prompt to be used")
	}
	if got, want := resp.GetText(), "20% of 85 is 17."; got != want {
		t.Fatalf("unexpected response text: got %q want %q", got, want)
	}
}

func TestChatIncludesResolvedSubjectContextInDecisionPrompt(t *testing.T) {
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
			if !strings.Contains(prompt, "Resolved subject context:\ncurrent_subject_id: person:mark\n- subject_id: person:mark aliases: mark") {
				t.Fatalf("decision prompt missing resolved subject context: %q", prompt)
			}
			if strings.Contains(prompt, "- subject_id: person:mark aliases: brother") || strings.Contains(prompt, "- subject_id: person:mark aliases: my brother") {
				t.Fatalf("relationship label leaked into identity aliases: %q", prompt)
			}
			return `[{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"kg","value":70}],"height":[{"unit":"cm","value":180}]}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			return "The BMI is 21.60.", nil
		},
	}

	resp, err := server.Chat(context.Background(), &pb.ChatRequest{
		SessionId: "test-session",
		Messages: []*pb.ChatMessage{
			{Role: "user", Content: "my brother Mark is 34 years old"},
			{Role: "assistant", Content: "Noted."},
			{Role: "user", Content: "what is his bmi at 70kg and 180cm?"},
		},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "The BMI is 21.60."; got != want {
		t.Fatalf("unexpected response text: got %q want %q", got, want)
	}
}

func TestChatHydratesCalculatorArgsFromSubjectMemoryAcrossTurns(t *testing.T) {
	t.Parallel()

	decisionCalls := 0
	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			decisionCalls++
			switch decisionCalls {
			case 1:
				return `[]`, nil
			case 2:
				return `[]`, nil
			case 3:
				if !strings.Contains(prompt, "current_subject_id: person:mark") {
					t.Fatalf("decision prompt missing current subject: %q", prompt)
				}
				return `[{"tool":"calculator","args":{"operation":"bmr"}}]`, nil
			default:
				t.Fatalf("unexpected decision call %d", decisionCalls)
				return "", nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			switch finalCalls {
			case 1:
				if !strings.Contains(prompt, "Tool result: tool=calculator result=BMR 1660.00 kcal/day") {
					t.Fatalf("final prompt missing hydrated BMR result: %q", prompt)
				}
				return "His BMR is 1660.00 kcal/day.", nil
			default:
				t.Fatalf("unexpected final call %d", finalCalls)
				return "", nil
			}
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("my brother Mark is a 34 year old male weighing 70kg and 180cm tall"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "Got it."; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequest("what is his bmr?"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "His BMR is 1660.00 kcal/day."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}
}

func TestChatRemembersStandaloneNamedSubjectAcrossTDEETurns(t *testing.T) {
	t.Parallel()

	decisionCalls := 0
	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			decisionCalls++
			switch decisionCalls {
			case 1:
				if !strings.Contains(prompt, "current_subject_id: person:serene") {
					t.Fatalf("first decision prompt missing direct named subject: %q", prompt)
				}
				return `[{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"lb","value":134}],"height":[{"unit":"cm","value":174}]}}]`, nil
			case 2:
				if !strings.Contains(prompt, "current_subject_id: person:serene") {
					t.Fatalf("second decision prompt missing direct named subject: %q", prompt)
				}
				return `[{"tool":"calculator","args":{"operation":"tdee"}}]`, nil
			case 3:
				if !strings.Contains(prompt, "current_subject_id: person:serene") {
					t.Fatalf("third decision prompt missing direct named subject: %q", prompt)
				}
				return `[{"tool":"calculator","args":{"operation":"tdee"}}]`, nil
			default:
				t.Fatalf("unexpected decision call %d", decisionCalls)
				return "", nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			switch finalCalls {
			case 1:
				return "The BMI of Serene is 20.08.", nil
			case 2:
				if !strings.Contains(prompt, "Tool result: tool=calculator result=TDEE 1924.06 kcal/day") {
					t.Fatalf("final prompt missing hydrated TDEE result: %q", prompt)
				}
				return "The TDEE for Serene is 1924.06 kcal/day.", nil
			case 3:
				if !strings.Contains(prompt, "Tool result: tool=calculator result=TDEE 1924.06 kcal/day") {
					t.Fatalf("repeat final prompt missing hydrated TDEE result: %q", prompt)
				}
				return "The TDEE for Serene is 1924.06 kcal/day.", nil
			default:
				t.Fatalf("unexpected final call %d", finalCalls)
				return "", nil
			}
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("what is the bmi of serene that has a height of 174cm and 134lbs?"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "The BMI of Serene is 20.08."; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequest("what is her tdee?"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "What are the age in years and gender?"; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}

	thirdResp, err := server.Chat(context.Background(), chatRequest("her gender is female and her age is 27"))
	if err != nil {
		t.Fatalf("third Chat returned error: %v", err)
	}
	if got, want := thirdResp.GetText(), "What is the activity level: sedentary, light, moderate, active, or very_active?"; got != want {
		t.Fatalf("unexpected third response: got %q want %q", got, want)
	}

	fourthResp, err := server.Chat(context.Background(), chatRequest("light"))
	if err != nil {
		t.Fatalf("fourth Chat returned error: %v", err)
	}
	if got, want := fourthResp.GetText(), "The TDEE for Serene is 1924.06 kcal/day."; got != want {
		t.Fatalf("unexpected fourth response: got %q want %q", got, want)
	}

	fifthResp, err := server.Chat(context.Background(), chatRequest("what is the tdee of serene?"))
	if err != nil {
		t.Fatalf("fifth Chat returned error: %v", err)
	}
	if got, want := fifthResp.GetText(), "The TDEE for Serene is 1924.06 kcal/day."; got != want {
		t.Fatalf("unexpected fifth response: got %q want %q", got, want)
	}
}

func TestChatCanonicalizesDuplicatedHealthMeasurementsBeforeExecution(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return `[{"tool":"calculator","args":{"operation":"bmr","weight":[{"unit":"lb","value":134},{"unit":"kg","value":60.88}],"height":[{"unit":"cm","value":174},{"unit":"m","value":1.74}],"age_years":27,"gender":"female"}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Tool result: tool=calculator result=BMR 1399.31 kcal/day") {
				t.Fatalf("final prompt missing canonicalized BMR result: %q", prompt)
			}
			return "Your BMR is 1399.31 kcal/day.", nil
		},
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is my bmr at 134lbs and 174cm if I am 27 years old and female?"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "Your BMR is 1399.31 kcal/day."; got != want {
		t.Fatalf("unexpected response text: got %q want %q", got, want)
	}
}

func TestChatRemembersPossessiveNamedSubjectAcrossHealthTurns(t *testing.T) {
	t.Parallel()

	decisionCalls := 0
	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			decisionCalls++
			switch decisionCalls {
			case 1:
				if !strings.Contains(prompt, "current_subject_id: person:serene") {
					t.Fatalf("first decision prompt missing possessive subject: %q", prompt)
				}
				return `[{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"lb","value":134}],"height":[{"unit":"cm","value":174}]}}]`, nil
			case 2:
				if !strings.Contains(prompt, "current_subject_id: person:serene") {
					t.Fatalf("second decision prompt missing possessive subject: %q", prompt)
				}
				return `[{"tool":"calculator","args":{"operation":"tdee","age_years":27,"gender":"female","activity_level":"light"}}]`, nil
			case 3:
				if !strings.Contains(prompt, "current_subject_id: person:serene") {
					t.Fatalf("third decision prompt missing pronoun-resolved subject: %q", prompt)
				}
				return `[{"tool":"calculator","args":{"operation":"bmr"}}]`, nil
			default:
				t.Fatalf("unexpected decision call %d", decisionCalls)
				return "", nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			switch finalCalls {
			case 1:
				return "Serene's BMI is 20.08.", nil
			case 2:
				if !strings.Contains(prompt, "Tool result: tool=calculator result=TDEE 1924.06 kcal/day") {
					t.Fatalf("final prompt missing hydrated TDEE result: %q", prompt)
				}
				return "Serene's TDEE is 1924.06 kcal/day.", nil
			case 3:
				if !strings.Contains(prompt, "Tool result: tool=calculator result=BMR 1399.31 kcal/day") {
					t.Fatalf("final prompt missing hydrated BMR result: %q", prompt)
				}
				return "Serene's BMR is 1399.31 kcal/day.", nil
			default:
				t.Fatalf("unexpected final call %d", finalCalls)
				return "", nil
			}
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("what is serene's bmi with 134lbs and 174cm?"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "Serene's BMI is 20.08."; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequest("what is serene's tdee? her activity level is light, she is female, and she is 27 years old"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "Serene's TDEE is 1924.06 kcal/day."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}

	thirdResp, err := server.Chat(context.Background(), chatRequest("what is her bmr?"))
	if err != nil {
		t.Fatalf("third Chat returned error: %v", err)
	}
	if got, want := thirdResp.GetText(), "Serene's BMR is 1399.31 kcal/day."; got != want {
		t.Fatalf("unexpected third response: got %q want %q", got, want)
	}
}

func TestChatPrefersIntroducedSpeakerSnapshotOverBadModelFallback(t *testing.T) {
	t.Parallel()

	decisionCalls := 0
	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			decisionCalls++
			switch decisionCalls {
			case 1:
				return `[{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"kg","value":45}],"height":[{"unit":"cm","value":162}]}}]`, nil
			case 2:
				return `[{"tool":"calculator","args":{"operation":"bmr","weight":[{"unit":"kg","value":45}],"height":[{"unit":"lb","value":45}]}}]`, nil
			default:
				t.Fatalf("unexpected decision call %d", decisionCalls)
				return "", nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			switch finalCalls {
			case 1:
				return "Your BMI is 17.15.", nil
			case 2:
				if !strings.Contains(prompt, "Tool result: tool=calculator result=BMR 1126.50 kcal/day") {
					t.Fatalf("final prompt missing hydrated self BMR result: %q", prompt)
				}
				return "Your BMR is 1126.50 kcal/day.", nil
			default:
				t.Fatalf("unexpected final call %d", finalCalls)
				return "", nil
			}
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("I'm Serene. what is my bmi with weight 45kg and height 162cm"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "Your BMI is 17.15."; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}
	if snapshot := server.memoryStore.Snapshot("test-session", "person:serene"); !strings.Contains(string(snapshot["height"]), `"value":162`) || !strings.Contains(string(snapshot["weight"]), `"value":45`) {
		t.Fatalf("unexpected speaker snapshot after bmi: %#v", snapshot)
	}

	secondResp, err := server.Chat(context.Background(), chatRequest("what is my bmr? I am 35 years old and female"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "Your BMR is 1126.50 kcal/day."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}
}

func TestChatPrefersNamedSubjectSnapshotOverBadModelFallback(t *testing.T) {
	t.Parallel()

	decisionCalls := 0
	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			decisionCalls++
			switch decisionCalls {
			case 1:
				return `[{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"lb","value":134}],"height":[{"unit":"cm","value":174}]}}]`, nil
			case 2:
				return `[{"tool":"calculator","args":{"operation":"bmr","weight":[{"unit":"kg","value":134}],"age_years":27,"gender":"female"}}]`, nil
			default:
				t.Fatalf("unexpected decision call %d", decisionCalls)
				return "", nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			switch finalCalls {
			case 1:
				return "Serene's BMI is 20.08.", nil
			case 2:
				if !strings.Contains(prompt, "Tool result: tool=calculator result=BMR 1399.31 kcal/day") {
					t.Fatalf("final prompt missing hydrated named-subject BMR result: %q", prompt)
				}
				return "Serene's BMR is 1399.31 kcal/day.", nil
			default:
				t.Fatalf("unexpected final call %d", finalCalls)
				return "", nil
			}
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("what is serene's bmi with weight 134lbs and 174cm?"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "Serene's BMI is 20.08."; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}
	if snapshot := server.memoryStore.Snapshot("test-session", "person:serene"); !strings.Contains(string(snapshot["height"]), `"value":174`) || !strings.Contains(string(snapshot["weight"]), `"value":60.78137758`) {
		t.Fatalf("unexpected serene snapshot after bmi: %#v", snapshot)
	}

	secondResp, err := server.Chat(context.Background(), chatRequest("what is her bmr? she is female and 27 years old"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "Serene's BMR is 1399.31 kcal/day."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}
}

func TestChatDoesNotLeakNamedSubjectDemographicsIntoSelfBMR(t *testing.T) {
	t.Parallel()

	decisionCalls := 0
	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			decisionCalls++
			switch decisionCalls {
			case 1:
				return `[{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"kg","value":45}],"height":[{"unit":"cm","value":162}]}}]`, nil
			case 2:
				return `[{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"lb","value":134}],"height":[{"unit":"cm","value":174}]}}]`, nil
			case 3:
				return `[{"tool":"calculator","args":{"operation":"tdee"}}]`, nil
			case 4:
				return `[{"tool":"calculator","args":{"operation":"bmr"}}]`, nil
			case 5:
				return `[{"tool":"calculator","args":{"operation":"bmr","weight":[{"unit":"kg","value":45},{"unit":"in","value":64}],"age_years":34,"gender":"female"}}]`, nil
			default:
				t.Fatalf("unexpected decision call %d", decisionCalls)
				return "", nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			switch finalCalls {
			case 1:
				return "Your BMI is 17.15.", nil
			case 2:
				return "Serene's BMI is 20.08.", nil
			case 3:
				if !strings.Contains(prompt, "Tool result: tool=calculator result=TDEE 1924.06 kcal/day") {
					t.Fatalf("final prompt missing serene TDEE result: %q", prompt)
				}
				return "Serene's TDEE is 1924.06 kcal/day.", nil
			case 4:
				if !strings.Contains(prompt, "Tool result: tool=calculator result=BMR 1399.31 kcal/day") {
					t.Fatalf("final prompt missing serene BMR result: %q", prompt)
				}
				return "Serene's BMR is 1399.31 kcal/day.", nil
			default:
				t.Fatalf("unexpected final call %d", finalCalls)
				return "", nil
			}
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("I'm Alex. what is my bmi? I weight 45kg and 162cm"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "Your BMI is 17.15."; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequest("what is serene's bmi? she weights 134lbs and is 174cm tall"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "Serene's BMI is 20.08."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}

	thirdResp, err := server.Chat(context.Background(), chatRequest("what is her tdee?"))
	if err != nil {
		t.Fatalf("third Chat returned error: %v", err)
	}
	if got, want := thirdResp.GetText(), "What are the age in years and gender?"; got != want {
		t.Fatalf("unexpected third response: got %q want %q", got, want)
	}

	fourthResp, err := server.Chat(context.Background(), chatRequest("she is 27 years old and female"))
	if err != nil {
		t.Fatalf("fourth Chat returned error: %v", err)
	}
	if got, want := fourthResp.GetText(), "What is the activity level: sedentary, light, moderate, active, or very_active?"; got != want {
		t.Fatalf("unexpected fourth response: got %q want %q", got, want)
	}

	fifthResp, err := server.Chat(context.Background(), chatRequest("light"))
	if err != nil {
		t.Fatalf("fifth Chat returned error: %v", err)
	}
	if got, want := fifthResp.GetText(), "Serene's TDEE is 1924.06 kcal/day."; got != want {
		t.Fatalf("unexpected fifth response: got %q want %q", got, want)
	}

	sixthResp, err := server.Chat(context.Background(), chatRequest("what is her bmr?"))
	if err != nil {
		t.Fatalf("sixth Chat returned error: %v", err)
	}
	if got, want := sixthResp.GetText(), "Serene's BMR is 1399.31 kcal/day."; got != want {
		t.Fatalf("unexpected sixth response: got %q want %q", got, want)
	}

	seventhResp, err := server.Chat(context.Background(), chatRequest("what is my bmr?"))
	if err != nil {
		t.Fatalf("seventh Chat returned error: %v", err)
	}
	if got, want := seventhResp.GetText(), "What are the age in years and gender?"; got != want {
		t.Fatalf("unexpected seventh response: got %q want %q", got, want)
	}
	if finalCalls != 4 {
		t.Fatalf("unexpected final call count: got %d want %d", finalCalls, 4)
	}
}

func TestChatAsksToDisambiguateConflictingMemoryBeforeBMR(t *testing.T) {
	t.Parallel()

	store := memoryctx.NewStore()
	if err := store.RememberUserMessage("test-session", "person:serene", "I weigh 45kg and I am 162cm tall"); err != nil {
		t.Fatalf("failed to preload first weight observation: %v", err)
	}
	if err := store.RememberUserMessage("test-session", "person:serene", "I weigh 50kg"); err != nil {
		t.Fatalf("failed to preload second weight observation: %v", err)
	}

	decisionCalls := 0
	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: store,
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			decisionCalls++
			switch decisionCalls {
			case 1:
				return `[{"tool":"calculator","args":{"operation":"bmr","age_years":35,"gender":"female","weight":[{"unit":"kg","value":45}]}}]`, nil
			default:
				t.Fatalf("unexpected decision call %d", decisionCalls)
				return "", nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			if !strings.Contains(prompt, "Tool result: tool=calculator result=BMR 1176.50 kcal/day") {
				t.Fatalf("final prompt missing disambiguated BMR result: %q", prompt)
			}
			return "Your BMR is 1176.50 kcal/day.", nil
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("I am Serene. what is my bmr? I am 35 years old and female"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "What is the weight?"; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}
	if finalCalls != 0 {
		t.Fatalf("final LLM should not run before disambiguation, got %d calls", finalCalls)
	}

	secondResp, err := server.Chat(context.Background(), chatRequest("50kg"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "Your BMR is 1176.50 kcal/day."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}
	if finalCalls != 1 {
		t.Fatalf("unexpected final call count: got %d want %d", finalCalls, 1)
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

func TestChatAbandonsPendingQuestionWhenResumeDoesNotReturnUsableCall(t *testing.T) {
	t.Parallel()

	freshCalls := 0
	finalCalls := 0
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
			if freshCalls == 1 {
				return `[{"tool":"calculator","args":{"operation":"bmi","height":[{"unit":"in","value":64}]}}]`, nil
			}
			return `[]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			if !strings.Contains(prompt, "Latest user reply: hmm") {
				t.Fatalf("final prompt missing latest reply after abandoning pending state: %q", prompt)
			}
			return "Okay.", nil
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
	if got, want := secondResp.GetText(), "Okay."; got != want {
		t.Fatalf("unexpected resumed response: got %q want %q", got, want)
	}
	if freshCalls != 3 {
		t.Fatalf("expected initial decision plus fresh/retry after abandoning pending state, got %d", freshCalls)
	}
	if finalCalls != 1 {
		t.Fatalf("unexpected final calls: %d", finalCalls)
	}
	if _, ok := server.getPending("test-session"); ok {
		t.Fatal("expected pending state to be cleared after abandoned resume")
	}
}

func TestChatInterruptsPendingQuestionForNewToolRequest(t *testing.T) {
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
			if strings.Contains(prompt, "Resume the pending tool call.") {
				t.Fatalf("resume LLM should not run for an obvious new tool request")
			}
			return `[{"tool":"calculator","args":{"operation":"bmi","height":[{"unit":"in","value":64}]}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			t.Fatalf("final LLM should not be called for interrupted time request: %q", prompt)
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

	secondResp, err := server.Chat(context.Background(), chatRequestWithSession("test-session", "what time is it?"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got := secondResp.GetText(); !strings.HasPrefix(got, "It is ") {
		t.Fatalf("unexpected interrupted response: %q", got)
	}
	if _, ok := server.getPending("test-session"); ok {
		t.Fatal("expected pending state to be cleared after interruption")
	}
}

func TestChatCoercesMemoryLookupBMIToCalculator(t *testing.T) {
	t.Parallel()

	memoryStore := memoryctx.NewStore()
	if err := memoryStore.RememberUserMessage("test-session", "person:serene", "My weight is 130lbs and my height is 5'8\""); err != nil {
		t.Fatalf("RememberUserMessage returned error: %v", err)
	}
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryStore,
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			switch {
			case strings.Contains(prompt, "do you remember my BMI?"):
				return `[{"tool":"memory_lookup","args":{"attribute":"BMI"}}]`, nil
			default:
				return `[]`, nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Tool result: tool=calculator result=BMI 19.77") {
				t.Fatalf("final prompt missing coerced BMI result: %q", prompt)
			}
			return "Your BMI is 19.77.", nil
		},
	}

	secondResp, err := server.Chat(context.Background(), chatRequest("I am Serene. do you remember my BMI?"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "Your BMI is 19.77."; got != want {
		t.Fatalf("unexpected BMI response: got %q want %q", got, want)
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

func TestChatUsesStoredSessionTranscriptForLatestOnlyFollowUp(t *testing.T) {
	t.Parallel()

	decisionCalls := 0
	finalCalls := 0
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
			switch decisionCalls {
			case 1:
				return `[{"tool":"calculator","args":{"operation":"bmr","weight":[{"unit":"kg","value":45}]}}]`, nil
			case 2:
				if !strings.Contains(prompt, "Active conversation thread:\nuser: what is the bmr of 45kg?\nassistant: What are the age in years, gender, and height?\nuser: 34 years, female, 162cm\nassistant: The BMR is 1131.50 kcal/day.\nuser: what is the tdee?") {
					t.Fatalf("decision prompt missing stored session transcript: %q", prompt)
				}
				return `[{"tool":"calculator","args":{"operation":"tdee","age_years":34,"gender":"female","weight":[{"unit":"kg","value":45}],"height":[{"unit":"cm","value":162}]}}]`, nil
			default:
				t.Fatalf("unexpected decision call %d with prompt %q", decisionCalls, prompt)
				return "", nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			switch finalCalls {
			case 1:
				if !strings.Contains(prompt, "Tool result: tool=calculator result=BMR 1131.50 kcal/day") {
					t.Fatalf("final prompt missing bmr result: %q", prompt)
				}
				return "The BMR is 1131.50 kcal/day.", nil
			default:
				t.Fatalf("final LLM should not be called for missing TDEE activity level")
				return "", nil
			}
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("what is the bmr of 45kg?"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "What are the age in years, gender, and height?"; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequestWithSession("test-session", "34 years, female, 162cm"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "The BMR is 1131.50 kcal/day."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}

	thirdResp, err := server.Chat(context.Background(), chatRequestWithSession("test-session", "what is the tdee?"))
	if err != nil {
		t.Fatalf("third Chat returned error: %v", err)
	}
	if got, want := thirdResp.GetText(), "What is the activity level: sedentary, light, moderate, active, or very_active?"; got != want {
		t.Fatalf("unexpected third response: got %q want %q", got, want)
	}
}

func TestChatUsesMemoryLookupForStoredHeight(t *testing.T) {
	t.Parallel()

	decisionCalls := 0
	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		factSelector: staticFactSelector{
			attr:  "height",
			attrs: []string{"weight", "height", "age_years", "gender", "activity_level"},
			facts: map[string]factsel.Fact{
				"height": {ID: "height", Kind: "measurement", OutputLabel: "height", QuestionLabel: "height"},
			},
		},
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			decisionCalls++
			switch decisionCalls {
			case 1:
				return `[{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"kg","value":45}],"height":[{"unit":"in","value":64}]}}]`, nil
			case 2:
				return `[{"tool":"memory_lookup","args":{}}]`, nil
			default:
				t.Fatalf("unexpected decision call %d with prompt %q", decisionCalls, prompt)
				return "", nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			if finalCalls == 1 {
				if !strings.Contains(prompt, "Tool result: tool=calculator result=BMI 17.03") {
					t.Fatalf("first final prompt missing bmi result: %q", prompt)
				}
				return "Your BMI is 17.03.", nil
			}
			t.Fatalf("unexpected final call %d with prompt %q", finalCalls, prompt)
			return "", nil
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("I'm Serene. what is my bmi? I weigh 45kg and am 64 inches tall"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "Your BMI is 17.03."; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequestWithSession("test-session", "what is my height?"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "Your height is 64 in."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}
	if got, want := finalCalls, 1; got != want {
		t.Fatalf("unexpected final calls: got %d want %d", got, want)
	}
}

func TestChatResolvesSimpleConvertFromInitialQuery(t *testing.T) {
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
			if strings.Contains(prompt, "Resume the pending tool call.") {
				t.Fatalf("resume LLM should not be needed when pending convert reply is explicit")
			}
			return `[{"tool":"calculator","args":{"operation":"convert","to_unit":"kg"}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Tool result: tool=calculator result=103 lb = 46.72001411 kg") {
				t.Fatalf("final prompt missing convert result: %q", prompt)
			}
			return "103 lb = 46.72001411 kg.", nil
		},
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is 103lbs in kg?"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "103 lb = 46.72001411 kg."; got != want {
		t.Fatalf("unexpected response: got %q want %q", got, want)
	}
}

func TestChatHandlesWeatherTool(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:          "http://llm.test/v1/chat/completions",
			LLMModel:            "test-model",
			LLMTimeoutMs:        500,
			WeatherGeocodingURL: "https://geocoding-api.open-meteo.com/v1/search",
		},
		tools: orchtools.NewLocalExecutorWithWeather(orchtools.WeatherConfig{
			HTTPURL:           "https://api.open-meteo.com/v1/forecast",
			GeocodingURL:      "https://geocoding-api.open-meteo.com/v1/search",
			TemperatureUnit:   "fahrenheit",
			WindSpeedUnit:     "mph",
			PrecipitationUnit: "inch",
			Now: func() time.Time {
				return time.Date(2026, 4, 23, 10, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
			},
			Fetch: func(ctx context.Context, requestURL string) ([]byte, error) {
				if strings.Contains(requestURL, "geocoding-api.open-meteo.com") {
					return []byte(`{"results":[{"name":"New York","latitude":40.7128,"longitude":-74.0060,"timezone":"America/New_York","country":"United States"}]}`), nil
				}
				return []byte(`{
					"timezone":"America/New_York",
					"current_units":{"temperature_2m":"°F"},
					"current":{"time":"2026-04-23T10:00","temperature_2m":68.4,"weather_code":2},
					"hourly_units":{"temperature_2m":"°F","precipitation_probability":"%"},
					"hourly":{"time":["2026-04-23T18:00"],"temperature_2m":[64.0],"precipitation_probability":[30],"weather_code":[3]},
					"daily_units":{"temperature_2m_max":"°F","temperature_2m_min":"°F","precipitation_probability_max":"%","precipitation_sum":"in"},
					"daily":{"time":["2026-04-23","2026-04-24"],"weather_code":[3,61],"temperature_2m_max":[72.1,66.0],"temperature_2m_min":[57.0,53.5],"precipitation_probability_max":[15,70],"precipitation_sum":[0.01,0.27]}
				}`), nil
			},
		}),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return `[{"tool":"weather","args":{"location":"New York"}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Tool result: tool=weather result=Tomorrow in New York, United States: rain, high 66°F, low 53.5°F, rain chance up to 70%.") {
				t.Fatalf("final prompt missing weather result: %q", prompt)
			}
			return "Tomorrow in New York, United States: rain, high 66°F, low 53.5°F, rain chance up to 70%.", nil
		},
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is the weather tomorrow?"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "Tomorrow in New York, United States: rain, high 66°F, low 53.5°F, rain chance up to 70%."; got != want {
		t.Fatalf("unexpected response: got %q want %q", got, want)
	}
}

func TestChatLearnsDefaultWeatherLocation(t *testing.T) {
	t.Parallel()

	fetch := func(ctx context.Context, requestURL string) ([]byte, error) {
		switch {
		case strings.Contains(requestURL, "geocoding-api.open-meteo.com"):
			return []byte(`{"results":[{"name":"Tokyo","latitude":35.6895,"longitude":139.6917,"timezone":"Asia/Tokyo","country":"Japan"}]}`), nil
		default:
			return []byte(`{
				"timezone":"Asia/Tokyo",
				"current_units":{"temperature_2m":"°F"},
				"current":{"time":"2026-04-23T10:00","temperature_2m":66.2,"weather_code":1},
				"hourly_units":{"temperature_2m":"°F","precipitation_probability":"%"},
				"hourly":{"time":["2026-04-23T18:00"],"temperature_2m":[61.0],"precipitation_probability":[20],"weather_code":[2]},
				"daily_units":{"temperature_2m_max":"°F","temperature_2m_min":"°F","precipitation_probability_max":"%","precipitation_sum":"in"},
				"daily":{"time":["2026-04-23","2026-04-24"],"weather_code":[1,3],"temperature_2m_max":[71.0,69.0],"temperature_2m_min":[58.0,57.0],"precipitation_probability_max":[15,20],"precipitation_sum":[0.0,0.02]}
			}`), nil
		}
	}

	decisionCalls := 0
	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:          "http://llm.test/v1/chat/completions",
			LLMModel:            "test-model",
			LLMTimeoutMs:        500,
			EmbeddingTimeoutMs:  500,
			WeatherGeocodingURL: "https://geocoding-api.open-meteo.com/v1/search",
		},
		tools: orchtools.NewLocalExecutorWithWeather(orchtools.WeatherConfig{
			HTTPURL:           "https://api.open-meteo.com/v1/forecast",
			GeocodingURL:      "https://geocoding-api.open-meteo.com/v1/search",
			TemperatureUnit:   "fahrenheit",
			WindSpeedUnit:     "mph",
			PrecipitationUnit: "inch",
			Now: func() time.Time {
				return time.Date(2026, 4, 23, 10, 0, 0, 0, time.FixedZone("JST", 9*60*60))
			},
			Fetch: fetch,
		}),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			decisionCalls++
			return `[{"tool":"weather","args":{}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			switch finalCalls {
			case 1:
				if !strings.Contains(prompt, "Tool result: tool=weather result=Today in Tokyo, Japan: mostly clear skies, high 71°F, low 58°F, rain chance up to 15%.") {
					t.Fatalf("first final prompt missing Tokyo forecast: %q", prompt)
				}
				return "Today in Tokyo, Japan: mostly clear skies, high 71°F, low 58°F, rain chance up to 15%.", nil
			case 2:
				if !strings.Contains(prompt, "Tool result: tool=weather result=Current temperature in Tokyo, Japan: 66.2°F.") {
					t.Fatalf("second final prompt missing stored weather location result: %q", prompt)
				}
				return "Current temperature in Tokyo, Japan: 66.2°F.", nil
			default:
				t.Fatalf("unexpected final call %d", finalCalls)
				return "", nil
			}
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("what is today's weather?"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "What location should I use for the weather?"; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequestWithSession("test-session", "Tokyo"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "Today in Tokyo, Japan: mostly clear skies, high 71°F, low 58°F, rain chance up to 15%."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}

	thirdResp, err := server.Chat(context.Background(), chatRequestWithSession("new-session", "what is the temperature?"))
	if err != nil {
		t.Fatalf("third Chat returned error: %v", err)
	}
	if got, want := thirdResp.GetText(), "What location should I use for the weather?"; got != want {
		t.Fatalf("unexpected third response: got %q want %q", got, want)
	}
}

func TestChatWeatherSupportsExplicitForecastHour(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools: orchtools.NewLocalExecutorWithWeather(orchtools.WeatherConfig{
			HTTPURL:           "https://api.open-meteo.com/v1/forecast",
			TemperatureUnit:   "fahrenheit",
			WindSpeedUnit:     "mph",
			PrecipitationUnit: "inch",
			Now: func() time.Time {
				return time.Date(2026, 4, 23, 10, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
			},
			Fetch: func(ctx context.Context, requestURL string) ([]byte, error) {
				return []byte(`{
					"timezone":"America/New_York",
					"current_units":{"temperature_2m":"°F"},
					"current":{"time":"2026-04-23T10:00","temperature_2m":68.4,"weather_code":2},
					"hourly_units":{"temperature_2m":"°F","precipitation_probability":"%"},
					"hourly":{"time":["2026-04-24T06:00"],"temperature_2m":[57.3],"precipitation_probability":[45],"weather_code":[3]},
					"daily_units":{"temperature_2m_max":"°F","temperature_2m_min":"°F","precipitation_probability_max":"%","precipitation_sum":"in"},
					"daily":{"time":["2026-04-23","2026-04-24"],"weather_code":[3,61],"temperature_2m_max":[72.1,66.0],"temperature_2m_min":[57.0,53.5],"precipitation_probability_max":[15,70],"precipitation_sum":[0.01,0.27]}
				}`), nil
			},
		}),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return `[{"tool":"weather","args":{"when":"tomorrow","focus":"temperature","hour_local":6,"location_name":"New York City","latitude":"40.7128","longitude":"-74.0060","timezone":"America/New_York"}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if !strings.Contains(prompt, "Tool result: tool=weather result=Tomorrow in New York City at 6 AM: 57.3°F.") {
				t.Fatalf("final prompt missing hourly weather result: %q", prompt)
			}
			return "Tomorrow in New York City at 6 AM: 57.3°F.", nil
		},
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is the temperature tomorrow at 6am?"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "Tomorrow in New York City at 6 AM: 57.3°F."; got != want {
		t.Fatalf("unexpected response: got %q want %q", got, want)
	}
}

func TestChatUsesSemanticMemoryRecallForStoredHeight(t *testing.T) {
	t.Parallel()

	store := memoryctx.NewStore().WithEmbeddings("http://embed.test/v1/embeddings", "test-embed", 0).WithEmbedder(func(httpURL, model string, inputs []string, timeout time.Duration) ([][]float32, error) {
		vectors := make([][]float32, 0, len(inputs))
		for _, input := range inputs {
			lower := strings.ToLower(input)
			switch {
			case strings.Contains(lower, "attribute: height"), strings.Contains(lower, "how tall"), strings.Contains(lower, "height?"):
				vectors = append(vectors, []float32{1, 0})
			case strings.Contains(lower, "attribute: weight"), strings.Contains(lower, "what is my weight"), strings.Contains(lower, "weigh"):
				vectors = append(vectors, []float32{0, 1})
			default:
				vectors = append(vectors, []float32{0.1, 0.1})
			}
		}
		return vectors, nil
	})

	decisionCalls := 0
	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:         "http://llm.test/v1/chat/completions",
			LLMModel:           "test-model",
			LLMTimeoutMs:       500,
			EmbeddingHTTPURL:   "http://embed.test/v1/embeddings",
			EmbeddingModel:     "test-embed",
			EmbeddingTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: store,
		factSelector: staticFactSelector{
			attr:  "",
			attrs: []string{"weight", "height", "age_years", "gender", "activity_level"},
			facts: map[string]factsel.Fact{
				"height": {ID: "height", Kind: "measurement", OutputLabel: "height", QuestionLabel: "height"},
			},
		},
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			decisionCalls++
			switch decisionCalls {
			case 1:
				return `[{"tool":"calculator","args":{"operation":"bmi","weight":[{"unit":"kg","value":45}],"height":[{"unit":"in","value":64}]}}]`, nil
			case 2:
				return `[{"tool":"memory_lookup","args":{}}]`, nil
			default:
				t.Fatalf("unexpected decision call %d with prompt %q", decisionCalls, prompt)
				return "", nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			if finalCalls == 1 {
				return "Your BMI is 17.03.", nil
			}
			t.Fatalf("unexpected final call %d with prompt %q", finalCalls, prompt)
			return "", nil
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("I'm Serene. what is my bmi? I weigh 45kg and am 64 inches tall"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "Your BMI is 17.03."; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequestWithSession("test-session", "how tall am i?"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "Your height is 64 in."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}
	if got, want := finalCalls, 1; got != want {
		t.Fatalf("unexpected final calls: got %d want %d", got, want)
	}
}

func TestChatStoresFirstPersonMeasurementsWithoutToolCall(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return `[]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if strings.Contains(prompt, "Tool result: tool=calculator result=BMI") {
				return "Your BMI is 19.77.", nil
			}
			return "Got it.", nil
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("I'm Serene. My weight is 130lbs and my height is 5'8\""))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "Got it."; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequest("do you remember my BMI?"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "Your BMI is 19.77."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}
}

func TestChatResolvesSpeakerAndGirlfriendBMIWithMixedUnits(t *testing.T) {
	t.Parallel()

	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return `[]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			switch {
			case strings.Contains(prompt, "Tool result: tool=calculator result=BMI 19.77"):
				return "Serene's BMI is 19.77.", nil
			case strings.Contains(prompt, "Tool result: tool=calculator result=BMI 17.53"):
				return "Sabrina's BMI is 17.53.", nil
			default:
				return "Got it.", nil
			}
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("I am Serene. My weight is 130lbs and my height is 5'8\""))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "Got it."; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequest("my girlfriend Sabrina is 46kg and 162cm tall"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "Got it."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}

	thirdResp, err := server.Chat(context.Background(), chatRequest("what is my BMI?"))
	if err != nil {
		t.Fatalf("third Chat returned error: %v", err)
	}
	if got, want := thirdResp.GetText(), "Serene's BMI is 19.77."; got != want {
		t.Fatalf("unexpected speaker BMI response: got %q want %q", got, want)
	}

	fourthResp, err := server.Chat(context.Background(), chatRequest("what is my girlfriend's BMI?"))
	if err != nil {
		t.Fatalf("fourth Chat returned error: %v", err)
	}
	if got, want := fourthResp.GetText(), "Sabrina's BMI is 17.53."; got != want {
		t.Fatalf("unexpected girlfriend BMI response: got %q want %q", got, want)
	}

	fifthResp, err := server.Chat(context.Background(), chatRequest("I am Sabrina"))
	if err != nil {
		t.Fatalf("fifth Chat returned error: %v", err)
	}
	if got, want := fifthResp.GetText(), "Got it."; got != want {
		t.Fatalf("unexpected speaker switch response: got %q want %q", got, want)
	}

	sixthResp, err := server.Chat(context.Background(), chatRequest("what is my girlfriend's BMI?"))
	if err != nil {
		t.Fatalf("sixth Chat returned error: %v", err)
	}
	if got, want := sixthResp.GetText(), "Who is your girlfriend?"; got != want {
		t.Fatalf("Sabrina should not inherit Serene's girlfriend relationship: got %q want %q", got, want)
	}

	seventhResp, err := server.Chat(context.Background(), chatRequest("my girlfriend Serene weighs 130lbs and is 5'8\""))
	if err != nil {
		t.Fatalf("seventh Chat returned error: %v", err)
	}
	if got, want := seventhResp.GetText(), "Got it."; got != want {
		t.Fatalf("unexpected Sabrina girlfriend storage response: got %q want %q", got, want)
	}

	eighthResp, err := server.Chat(context.Background(), chatRequest("what is my girlfriend's BMI?"))
	if err != nil {
		t.Fatalf("eighth Chat returned error: %v", err)
	}
	if got, want := eighthResp.GetText(), "Serene's BMI is 19.77."; got != want {
		t.Fatalf("unexpected Sabrina-scoped girlfriend BMI response: got %q want %q", got, want)
	}

	restartedServer := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: server.memoryStore,
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return `[]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			t.Fatalf("permanent identity relationship should avoid final LLM attribution: %q", prompt)
			return "", nil
		},
	}
	ninthResp, err := restartedServer.Chat(context.Background(), chatRequest("what is my girlfriend's BMI?"))
	if err != nil {
		t.Fatalf("ninth Chat returned error: %v", err)
	}
	if got, want := ninthResp.GetText(), "Serene's BMI is 19.77."; got != want {
		t.Fatalf("unexpected restarted girlfriend BMI response: got %q want %q", got, want)
	}

	serene := server.memoryStore.Snapshot("test-session", "person:serene", "weight", "height")
	if !strings.Contains(string(serene["weight"]), `"value":58.9670081`) || !strings.Contains(string(serene["height"]), `"value":172.72`) {
		t.Fatalf("unexpected Serene measurements: %#v", serene)
	}
	sereneGirlfriend := server.memoryStore.Snapshot("test-session", "scoped:person_serene:girlfriend:sabrina", "weight", "height")
	if !strings.Contains(string(sereneGirlfriend["weight"]), `"value":46`) || !strings.Contains(string(sereneGirlfriend["height"]), `"value":162`) {
		t.Fatalf("unexpected Serene-scoped girlfriend measurements: %#v", sereneGirlfriend)
	}
	sabrina := server.memoryStore.Snapshot("test-session", "person:sabrina", "weight", "height")
	if len(sabrina) != 0 {
		t.Fatalf("Sabrina's standalone identity should not inherit Serene-scoped facts: %#v", sabrina)
	}
	sabrinaGirlfriend := server.memoryStore.Snapshot("test-session", "scoped:person_sabrina:girlfriend:serene", "weight", "height")
	if !strings.Contains(string(sabrinaGirlfriend["weight"]), `"value":58.9670081`) || !strings.Contains(string(sabrinaGirlfriend["height"]), `"value":172.72`) {
		t.Fatalf("unexpected Sabrina-scoped girlfriend measurements: %#v", sabrinaGirlfriend)
	}
	if finalCalls != 1 {
		t.Fatalf("unexpected final call count: got %d want %d", finalCalls, 1)
	}
}

func TestChatStoresNamedBranchFactsUnderActiveSpeakerTree(t *testing.T) {
	t.Parallel()

	decisionCalls := 0
	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			decisionCalls++
			switch decisionCalls {
			case 1:
				if !strings.Contains(prompt, "current_subject_id: scoped:person_serene:girlfriend:sabrina") {
					t.Fatalf("BMI prompt missing Serene-scoped Sabrina subject: %q", prompt)
				}
				return `[{"tool":"calculator","args":{"operation":"bmi"}}]`, nil
			default:
				t.Fatalf("unexpected decision call %d", decisionCalls)
				return "", nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			switch finalCalls {
			case 1:
				if !strings.Contains(prompt, "Tool result: tool=calculator result=BMI 17.53") {
					t.Fatalf("final prompt missing scoped Sabrina BMI result: %q", prompt)
				}
				return "Sabrina's BMI is 17.53.", nil
			default:
				t.Fatalf("unexpected final call %d", finalCalls)
				return "", nil
			}
		},
	}

	for idx, text := range []string{
		"I am Serene. My girlfriend is Sabrina",
		"I am Sabrina",
		"Hey BeeMo, it's Serene again",
		"Sabrina weighs 46kg and is 162cm tall",
	} {
		resp, err := server.Chat(context.Background(), chatRequest(text))
		if err != nil {
			t.Fatalf("Chat %d returned error: %v", idx+1, err)
		}
		if got, want := resp.GetText(), "Got it."; got != want {
			t.Fatalf("unexpected response %d: got %q want %q", idx+1, got, want)
		}
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is Sabrina's BMI?"))
	if err != nil {
		t.Fatalf("BMI Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "Sabrina's BMI is 17.53."; got != want {
		t.Fatalf("unexpected named branch BMI: got %q want %q", got, want)
	}

	sereneGirlfriend := server.memoryStore.Snapshot("test-session", "scoped:person_serene:girlfriend:sabrina", "weight", "height")
	if !strings.Contains(string(sereneGirlfriend["weight"]), `"value":46`) || !strings.Contains(string(sereneGirlfriend["height"]), `"value":162`) {
		t.Fatalf("unexpected Serene-scoped girlfriend measurements: %#v", sereneGirlfriend)
	}
	standaloneSabrina := server.memoryStore.Snapshot("test-session", "person:sabrina", "weight", "height")
	if len(standaloneSabrina) != 0 {
		t.Fatalf("Sabrina's standalone identity should not receive Serene-scoped girlfriend facts: %#v", standaloneSabrina)
	}
}

func TestChatStoresIntroRelationshipMeasurementsOnRelationshipTarget(t *testing.T) {
	t.Parallel()

	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return `[]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			switch {
			case strings.Contains(prompt, "Tool result: tool=calculator result=BMI 17.53"):
				return "Maureen's BMI is 17.53.", nil
			default:
				return "Got it.", nil
			}
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("I am Sabrina. My mom is Maureen. Maureen weighs 46kg and is 162cm tall"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "Got it."; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}
	if speakerID, ok, err := server.memoryStore.ActiveSpeaker("test-session"); err != nil {
		t.Fatalf("ActiveSpeaker returned error: %v", err)
	} else if !ok || speakerID != "person:sabrina" {
		t.Fatalf("unexpected active speaker: got %q ok=%v", speakerID, ok)
	}
	maureen := server.memoryStore.Snapshot("test-session", "scoped:person_sabrina:mother:maureen", "weight", "height")
	if !strings.Contains(string(maureen["weight"]), `"value":46`) || !strings.Contains(string(maureen["height"]), `"value":162`) {
		t.Fatalf("unexpected Maureen measurements: %#v", maureen)
	}
	standaloneMaureen := server.memoryStore.Snapshot("test-session", "person:maureen", "weight", "height")
	if len(standaloneMaureen) != 0 {
		t.Fatalf("Maureen's standalone identity should not receive Sabrina-scoped mom facts: %#v", standaloneMaureen)
	}
	sabrina := server.memoryStore.Snapshot("test-session", "person:sabrina", "weight", "height")
	if len(sabrina) != 0 {
		t.Fatalf("Sabrina should not receive Maureen's measurements: %#v", sabrina)
	}

	secondResp, err := server.Chat(context.Background(), chatRequest("what is my mom BMI?"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "Maureen's BMI is 17.53."; got != want {
		t.Fatalf("unexpected mom BMI response: got %q want %q", got, want)
	}
	if finalCalls != 0 {
		t.Fatalf("unexpected final call count: got %d want %d", finalCalls, 0)
	}
}

func TestChatUsesDeterministicCalculatorForScopedRelationshipBMI(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:         "http://llm.test/v1/chat/completions",
			LLMModel:           "test-model",
			LLMTimeoutMs:       500,
			EmbeddingTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		routeSelector: staticRouteSelector{
			candidates: []routing.Candidate{
				{
					Route: routing.Route{
						ID:          "calculator.bmi",
						Domain:      "calculator",
						Handler:     routing.Handler{Type: "tool", Target: "calculator"},
						DefaultArgs: map[string]any{"operation": "bmi"},
						Memory: routing.MemoryPolicy{
							Read:  true,
							Write: true,
							Attrs: []string{"weight", "height"},
							Scope: "subject",
						},
					},
				},
			},
		},
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			t.Fatalf("scoped relationship BMI should not need decision LLM: %q", prompt)
			return "", nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			t.Fatalf("scoped relationship BMI should answer directly: %q", prompt)
			return "", nil
		},
	}

	for _, text := range []string{
		"I am Serene. My girlfriend is Sabrina",
		"Sabrina weighs 46kg and is 162cm tall",
	} {
		resp, err := server.Chat(context.Background(), chatRequest(text))
		if err != nil {
			t.Fatalf("Chat(%q) returned error: %v", text, err)
		}
		if got, want := resp.GetText(), "Got it."; got != want {
			t.Fatalf("unexpected response for %q: got %q want %q", text, got, want)
		}
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is my girlfriend's BMI?"))
	if err != nil {
		t.Fatalf("girlfriend BMI Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "Sabrina's BMI is 17.53."; got != want {
		t.Fatalf("unexpected girlfriend BMI: got %q want %q", got, want)
	}
}

func TestChatSwitchesBackToSavedIdentityTreeWithAgainIntro(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return `[]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			t.Fatalf("scoped identity tree should answer directly without final LLM attribution: %q", prompt)
			return "", nil
		},
	}

	for idx, text := range []string{
		"I am Serene. My mom Nicole weighs 60kg and is 170cm tall",
		"I am Sabrina. My mom Maureen weighs 46kg and is 162cm tall",
		"Hey BeeMo, it's Serene again",
	} {
		resp, err := server.Chat(context.Background(), chatRequest(text))
		if err != nil {
			t.Fatalf("Chat %d returned error: %v", idx+1, err)
		}
		if got, want := resp.GetText(), "Got it."; got != want {
			t.Fatalf("unexpected response %d: got %q want %q", idx+1, got, want)
		}
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is my mom BMI?"))
	if err != nil {
		t.Fatalf("mom BMI Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "Nicole's BMI is 20.76."; got != want {
		t.Fatalf("unexpected restored Serene mom BMI: got %q want %q", got, want)
	}

	sereneMom := server.memoryStore.Snapshot("test-session", "scoped:person_serene:mother:nicole", "weight", "height")
	if !strings.Contains(string(sereneMom["weight"]), `"value":60`) || !strings.Contains(string(sereneMom["height"]), `"value":170`) {
		t.Fatalf("unexpected Serene mom measurements: %#v", sereneMom)
	}
	sabrinaMom := server.memoryStore.Snapshot("test-session", "scoped:person_sabrina:mother:maureen", "weight", "height")
	if !strings.Contains(string(sabrinaMom["weight"]), `"value":46`) || !strings.Contains(string(sabrinaMom["height"]), `"value":162`) {
		t.Fatalf("unexpected Sabrina mom measurements: %#v", sabrinaMom)
	}
}

func TestChatAcknowledgesSpeakerSwitchWithoutBorrowingPreviousTopic(t *testing.T) {
	t.Parallel()

	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return `[]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			return "stale BMI answer", nil
		},
	}

	if _, err := server.Chat(context.Background(), chatRequest("I am Sabrina. My mom is Maureen. Maureen weighs 46kg and is 162cm tall")); err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	resp, err := server.Chat(context.Background(), chatRequest("I am Serene"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "Got it."; got != want {
		t.Fatalf("unexpected speaker switch response: got %q want %q", got, want)
	}
	if finalCalls != 0 {
		t.Fatalf("speaker switch should not call final LLM, got %d calls", finalCalls)
	}
}

func TestChatDoesNotExposeOldMemoryToUnrelatedGenericReply(t *testing.T) {
	t.Parallel()

	finalCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		factSelector: staticFactSelector{
			attr:  "birthday",
			attrs: []string{"birthday"},
			facts: map[string]factsel.Fact{
				"birthday": {ID: "birthday", Kind: "text", OutputLabel: "birthday", QuestionLabel: "birthday"},
			},
		},
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			if strings.Contains(prompt, "birthday") && strings.Contains(prompt, "my birthday is June 4") {
				return `[{"tool":"memory_lookup","args":{"attribute":"birthday"}}]`, nil
			}
			return `[]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			finalCalls++
			if strings.Contains(prompt, "June 4") {
				t.Fatalf("unrelated final prompt exposed old birthday memory: %q", prompt)
			}
			return "Short answer.", nil
		},
	}

	firstResp, err := server.Chat(context.Background(), chatRequest("I'm Serene. my birthday is June 4"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := firstResp.GetText(), "Got it."; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	secondResp, err := server.Chat(context.Background(), chatRequest("tell me something short"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := secondResp.GetText(), "Short answer."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}
	if got, want := finalCalls, 1; got != want {
		t.Fatalf("unexpected final calls: got %d want %d", got, want)
	}
}

func TestChatIgnoresSubjectIDMemoryLookupOnSpeakerIntroduction(t *testing.T) {
	t.Parallel()

	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			return `[{"tool":"memory_lookup","args":{"attribute":"person:serene"}}]`, nil
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			if strings.Contains(prompt, `Tool result: tool=memory_lookup`) {
				t.Fatalf("identity-shaped memory_lookup reached final tool result: %q", prompt)
			}
			return "Got it.", nil
		},
	}

	resp, err := server.Chat(context.Background(), chatRequest("Hi BMO, I'm Serene. My weight is 130lbs and my height is 5'8\""))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "Got it."; got != want {
		t.Fatalf("unexpected response: got %q want %q", got, want)
	}
	if pending, ok := server.getPending("test-session"); ok {
		t.Fatalf("unexpected pending tool: %#v", pending)
	}
	if speakerID, ok, err := server.memoryStore.ActiveSpeaker("test-session"); err != nil {
		t.Fatalf("ActiveSpeaker returned error: %v", err)
	} else if !ok || speakerID != "person:serene" {
		t.Fatalf("unexpected active speaker: got %q ok=%v", speakerID, ok)
	}
	serene := server.memoryStore.Snapshot("test-session", "person:serene", "weight", "height")
	if !strings.Contains(string(serene["weight"]), `"value":58.9670081`) || !strings.Contains(string(serene["height"]), `"value":172.72`) {
		t.Fatalf("unexpected Serene measurements: %#v", serene)
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

func TestChatIdentitySwitchClearsPendingToolState(t *testing.T) {
	t.Parallel()

	decisionCalls := 0
	server := &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:   "http://llm.test/v1/chat/completions",
			LLMModel:     "test-model",
			LLMTimeoutMs: 500,
		},
		tools:       orchtools.NewLocalExecutor(),
		memoryStore: memoryctx.NewStore(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			decisionCalls++
			switch decisionCalls {
			case 1:
				return `[{"tool":"calculator","args":{"operation":"tdee"}}]`, nil
			case 2:
				return `[{"tool":"calculator","args":{"operation":"bmi"}}]`, nil
			default:
				t.Fatalf("unexpected decision call %d", decisionCalls)
				return "", nil
			}
		},
		callFinalMessage: func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
			t.Fatalf("final LLM should not be called: %q", prompt)
			return "", nil
		},
	}

	for _, text := range []string{
		"I am Sabrina. My mom Maureen weighs 70kg and is 168cm tall",
		"I am Serene",
	} {
		resp, err := server.Chat(context.Background(), chatRequest(text))
		if err != nil {
			t.Fatalf("Chat(%q) returned error: %v", text, err)
		}
		if got, want := resp.GetText(), "Got it."; got != want {
			t.Fatalf("unexpected response for %q: got %q want %q", text, got, want)
		}
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is my tdee?"))
	if err != nil {
		t.Fatalf("tdee Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "What are the age in years, gender, weight, and height?"; got != want {
		t.Fatalf("unexpected tdee clarification: got %q want %q", got, want)
	}
	if _, ok := server.getPending("test-session"); !ok {
		t.Fatal("expected pending TDEE state")
	}

	resp, err = server.Chat(context.Background(), chatRequest("Hey BeeMo, it is Sabrina again"))
	if err != nil {
		t.Fatalf("identity switch Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "Got it."; got != want {
		t.Fatalf("unexpected identity switch response: got %q want %q", got, want)
	}
	if _, ok := server.getPending("test-session"); ok {
		t.Fatal("identity switch should clear pending tool state")
	}

	resp, err = server.Chat(context.Background(), chatRequest("what is my mom BMI?"))
	if err != nil {
		t.Fatalf("mom BMI Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "Maureen's BMI is 24.80."; got != want {
		t.Fatalf("unexpected mom BMI after identity switch: got %q want %q", got, want)
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

func TestDirectMemoryResponseKeepsMultiwordLabels(t *testing.T) {
	t.Parallel()

	got, ok := directToolResponse([]orchtools.Result{
		{Action: "memory_lookup", Output: "favorite food: mango rice"},
	}, "what is my favorite food?")
	if !ok {
		t.Fatal("expected direct memory response")
	}
	if want := "Your favorite food is mango rice."; got != want {
		t.Fatalf("unexpected direct response: got %q want %q", got, want)
	}
}

func TestDirectTimeResponseFormatsCurrentTimeWithoutLLM(t *testing.T) {
	t.Parallel()

	got, ok := directToolResponse([]orchtools.Result{
		{Action: "get_time", Output: "2026-05-17T08:16:00-04:00"},
	}, "what time is it?")
	if !ok {
		t.Fatal("expected direct time response")
	}
	if want := "It is 8:16 AM on May 17, 2026."; got != want {
		t.Fatalf("unexpected direct time response: got %q want %q", got, want)
	}
}

func TestDirectTimeResponseSkipsRelativeDateQuestions(t *testing.T) {
	t.Parallel()

	if _, ok := directToolResponse([]orchtools.Result{
		{Action: "get_time", Output: "2026-05-17T08:16:00-04:00"},
	}, "what date will it be 5 days from today?"); ok {
		t.Fatal("relative date questions should use the final response path")
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
