package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
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

type queryRecordingRouteSelector struct {
	queries    []string
	candidates []routing.Candidate
}

func (s *queryRecordingRouteSelector) Retrieve(query string, timeout time.Duration) ([]routing.Candidate, error) {
	s.queries = append(s.queries, query)
	return s.candidates, nil
}

type recordingExecutor struct {
	calls  []orchtools.Request
	result orchtools.Result
}

func (e *recordingExecutor) Execute(ctx context.Context, req orchtools.Request) (orchtools.Result, error) {
	e.calls = append(e.calls, req)
	result := e.result
	if result.Action == "" {
		result.Action = req.Action
	}
	return result, nil
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

func TestChatScopesTranscriptToCurrentSession(t *testing.T) {
	t.Parallel()

	finalPrompts := []string{}
	server := testServer(t)
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		return `[]`, nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		finalPrompts = append(finalPrompts, prompt)
		switch len(finalPrompts) {
		case 1:
			return "Got it.", nil
		case 2:
			return "Your girlfriend's name is Sabrina.", nil
		default:
			return "I do not know from this session.", nil
		}
	}

	first, err := server.Chat(context.Background(), sessionChatRequest("serene-session", "My girlfriend is Sabrina."))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := first.GetText(), "Got it."; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	second, err := server.Chat(context.Background(), sessionChatRequest("serene-session", "What is my girlfriend's name?"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := second.GetText(), "Your girlfriend's name is Sabrina."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}
	if len(finalPrompts) < 2 {
		t.Fatalf("expected at least two final prompts, got %d", len(finalPrompts))
	}
	if !strings.Contains(finalPrompts[1], "user: My girlfriend is Sabrina.") {
		t.Fatalf("same-session prompt missing prior user fact: %q", finalPrompts[1])
	}

	third, err := server.Chat(context.Background(), sessionChatRequest("fresh-session", "What is my girlfriend's name?"))
	if err != nil {
		t.Fatalf("third Chat returned error: %v", err)
	}
	if got, want := third.GetText(), "I do not know from this session."; got != want {
		t.Fatalf("unexpected third response: got %q want %q", got, want)
	}
	if len(finalPrompts) < 3 {
		t.Fatalf("expected three final prompts, got %d", len(finalPrompts))
	}
	if strings.Contains(finalPrompts[2], "My girlfriend is Sabrina") {
		t.Fatalf("fresh-session prompt leaked prior session context: %q", finalPrompts[2])
	}
}

func TestChatWeatherParsesLocationAndTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		question       string
		wantToolResult string
		wantAnswer     string
	}{
		{
			name:           "tomorrow_new_york_city",
			question:       "what is the weather tomorrow in new york city?",
			wantToolResult: "tool=weather result=Tomorrow in New York, United States: clear skies, high 75°F, low 62°F, rain chance up to 10%.",
			wantAnswer:     "Tomorrow in New York, United States: clear skies, high 75°F, low 62°F, rain chance up to 10%.",
		},
		{
			name:           "tomorrow_6am_temperature_new_york_city",
			question:       "what temperature will it be tomorrow at 6am in new york city?",
			wantToolResult: "tool=weather result=Tomorrow in New York, United States at 6 AM: 58°F.",
			wantAnswer:     "Tomorrow in New York, United States at 6 AM: 58°F.",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := testServer(t)
			server.tools = orchtools.NewLocalExecutorWithWeather(mockNewYorkWeatherConfig(t))
			server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
				t.Fatalf("decision LLM should not be needed for weather inference: %q", prompt)
				return "", nil
			}
			server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
				if !strings.Contains(prompt, tt.wantToolResult) {
					t.Fatalf("final prompt missing weather result\nwant: %s\nprompt: %q", tt.wantToolResult, prompt)
				}
				return tt.wantAnswer, nil
			}

			resp, err := server.Chat(context.Background(), chatRequest(tt.question))
			if err != nil {
				t.Fatalf("Chat returned error: %v", err)
			}
			if got := resp.GetText(); got != tt.wantAnswer {
				t.Fatalf("unexpected weather response: got %q want %q", got, tt.wantAnswer)
			}
		})
	}
}

func TestChatWeatherMissingLocationClarifies(t *testing.T) {
	t.Parallel()

	server := testServer(t)
	server.tools = orchtools.NewLocalExecutorWithWeather(orchtools.WeatherConfig{
		HTTPURL: "https://forecast.test/v1/forecast",
		Now: func() time.Time {
			return time.Date(2026, 5, 17, 12, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
		},
	})
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		t.Fatalf("decision LLM should not be needed for weather inference: %q", prompt)
		return "", nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		t.Fatalf("final LLM should not be called for missing weather location: %q", prompt)
		return "", nil
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is the weather tomorrow?"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "What location should I use for the weather?"; got != want {
		t.Fatalf("unexpected clarification: got %q want %q", got, want)
	}
}

func TestChatWeatherPendingFullRetryExtractsLocation(t *testing.T) {
	t.Parallel()

	server := testServer(t)
	server.tools = orchtools.NewLocalExecutorWithWeather(mockNewYorkWeatherConfig(t))
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		t.Fatalf("decision LLM should not be needed for weather pending fill: %q", prompt)
		return "", nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		if !strings.Contains(prompt, "Tomorrow in New York, United States") {
			t.Fatalf("final prompt missing weather result: %q", prompt)
		}
		return "Tomorrow in New York, United States: clear skies, high 75°F, low 62°F, rain chance up to 10%.", nil
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is the weather tomorrow?"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "What location should I use for the weather?"; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	resp, err = server.Chat(context.Background(), chatRequest("what is the weather tomorrow in new york city?"))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got := resp.GetText(); !strings.Contains(got, "New York") {
		t.Fatalf("unexpected second response: %q", got)
	}
}

func TestChatWeatherBadLocationClarifiesWithoutError(t *testing.T) {
	t.Parallel()

	server := testServer(t)
	server.tools = orchtools.NewLocalExecutorWithWeather(mockNewYorkWeatherConfig(t))
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		t.Fatalf("decision LLM should not be needed for weather inference: %q", prompt)
		return "", nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		t.Fatalf("final LLM should not be called for bad weather location: %q", prompt)
		return "", nil
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is the weather tomorrow in adisson new jersey?"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "I could not find that location. What city and state should I use for the weather?"; got != want {
		t.Fatalf("unexpected clarification: got %q want %q", got, want)
	}
}

func TestChatCalculatorFixedNumberSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		question       string
		decision       string
		wantToolResult string
		wantAnswer     string
	}{
		{
			name:           "percent_of_20_85",
			question:       "what is 20 percent of 85?",
			wantToolResult: "tool=calculator result=20% of 85 = 17",
			wantAnswer:     "20% of 85 is 17.",
		},
		{
			name:           "percent_of_15_240",
			question:       "what is 15 percent of 240?",
			wantToolResult: "tool=calculator result=15% of 240 = 36",
			wantAnswer:     "15% of 240 is 36.",
		},
		{
			name:           "percent_of_7_5_320",
			question:       "what is 7.5 percent of 320?",
			wantToolResult: "tool=calculator result=7.5% of 320 = 24",
			wantAnswer:     "7.5% of 320 is 24.",
		},
		{
			name:           "percent_change_increase",
			question:       "increase 85 by 12 percent",
			decision:       `[{"tool":"calculator","args":{"operation":"percent_change","value":85,"percent":12,"direction":"increase"}}]`,
			wantToolResult: "tool=calculator result=85 increased by 12% = 95.2",
			wantAnswer:     "85 increased by 12% is 95.2.",
		},
		{
			name:           "percent_ratio",
			question:       "what percentage is 18 of 24?",
			decision:       `[{"tool":"calculator","args":{"operation":"percent_ratio","part":18,"whole":24}}]`,
			wantToolResult: "tool=calculator result=18 is 75% of 24",
			wantAnswer:     "18 is 75% of 24.",
		},
		{
			name:           "height_conversion",
			question:       "convert 5 foot 4 to centimeters",
			wantToolResult: "tool=calculator result=5 ft 4 in = 162.56 cm",
			wantAnswer:     "5 ft 4 in is 162.56 cm.",
		},
		{
			name:           "weight_conversion",
			question:       "convert 103 pounds to kilograms",
			wantToolResult: "tool=calculator result=103 lb = 46.72001411 kg",
			wantAnswer:     "103 lb is 46.72001411 kg.",
		},
		{
			name:           "distance_conversion",
			question:       "convert 10 miles to kilometers",
			wantToolResult: "tool=calculator result=10 mi = 16.09344 km",
			wantAnswer:     "10 mi is 16.09344 km.",
		},
		{
			name:           "volume_conversion",
			question:       "convert 2 liters to milliliters",
			wantToolResult: "tool=calculator result=2 l = 2000 ml",
			wantAnswer:     "2 l is 2000 ml.",
		},
		{
			name:           "time_conversion",
			question:       "convert 90 minutes to hours",
			wantToolResult: "tool=calculator result=90 min = 1.5 hr",
			wantAnswer:     "90 min is 1.5 hr.",
		},
		{
			name:           "small_mass_conversion",
			question:       "convert 2500 milligrams to grams",
			wantToolResult: "tool=calculator result=2500 mg = 2.5 g",
			wantAnswer:     "2500 mg is 2.5 g.",
		},
		{
			name:           "speed_conversion",
			question:       "convert 10 mph to kilometers per hour",
			wantToolResult: "tool=calculator result=10 mi/hr = 16.09344 km/hr",
			wantAnswer:     "10 mi/hr is 16.09344 km/hr.",
		},
		{
			name:           "temperature_conversion",
			question:       "convert 98.6 fahrenheit to celsius",
			wantToolResult: "tool=calculator result=98.6 f = 37 c",
			wantAnswer:     "98.6 f is 37 c.",
		},
		{
			name:           "bmi_imperial",
			question:       "what is my BMI at 130lbs and 5'8\"?",
			wantToolResult: "tool=calculator result=BMI 19.77",
			wantAnswer:     "Your BMI is 19.77.",
		},
		{
			name:           "bmi_metric",
			question:       "what is BMI for 46kg and 162cm?",
			wantToolResult: "tool=calculator result=BMI 17.53",
			wantAnswer:     "The BMI is 17.53.",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := testServer(t)
			server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
				if strings.TrimSpace(tt.decision) == "" {
					t.Fatalf("decision LLM should not be needed for %q; prompt=%q", tt.question, prompt)
				}
				return tt.decision, nil
			}
			server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
				if !strings.Contains(prompt, tt.wantToolResult) {
					t.Fatalf("final prompt missing calculator result\nwant: %s\nprompt: %q", tt.wantToolResult, prompt)
				}
				return tt.wantAnswer, nil
			}

			resp, err := server.Chat(context.Background(), chatRequest(tt.question))
			if err != nil {
				t.Fatalf("Chat returned error: %v", err)
			}
			if got := resp.GetText(); got != tt.wantAnswer {
				t.Fatalf("unexpected calculator response: got %q want %q", got, tt.wantAnswer)
			}
		})
	}
}

func TestChatBMIClarificationResume(t *testing.T) {
	t.Parallel()

	server := testServer(t)
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		t.Fatalf("decision LLM should not be needed for BMI clarification flow: %q", prompt)
		return "", nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		if !strings.Contains(prompt, "Tool result: tool=calculator result=BMI 19.77") {
			t.Fatalf("final prompt missing resumed BMI result: %q", prompt)
		}
		return "Your BMI is 19.77.", nil
	}

	first, err := server.Chat(context.Background(), chatRequest("what is my BMI if I weigh 130lbs?"))
	if err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if got, want := first.GetText(), "What is the height?"; got != want {
		t.Fatalf("unexpected first response: got %q want %q", got, want)
	}

	second, err := server.Chat(context.Background(), chatRequest("5'8\""))
	if err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}
	if got, want := second.GetText(), "Your BMI is 19.77."; got != want {
		t.Fatalf("unexpected second response: got %q want %q", got, want)
	}
}

func TestChatRoutesExpertAndWritingRequestsToOlderSister(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		question string
		answer   string
	}{
		{
			name:     "expert_knowledge",
			question: "what is the circumference of the earth?",
			answer:   "Earth's circumference is about 40,075 km at the equator.",
		},
		{
			name:     "sentence_improvement",
			question: "please improve this sentence: i went store and it was good",
			answer:   "I went to the store, and it was a good experience.",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			executor := &recordingExecutor{
				result: orchtools.Result{Output: tt.answer},
			}
			server := testServer(t)
			server.tools = executor
			server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
				t.Fatalf("decision LLM should not be needed for older_sister fallback: %q", prompt)
				return "", nil
			}
			server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
				want := "Tool result: tool=older_sister result=" + tt.answer
				if !strings.Contains(prompt, want) {
					t.Fatalf("final prompt missing older_sister result\nwant: %s\nprompt: %q", want, prompt)
				}
				return tt.answer, nil
			}

			resp, err := server.Chat(context.Background(), chatRequest(tt.question))
			if err != nil {
				t.Fatalf("Chat returned error: %v", err)
			}
			if got := resp.GetText(); got != tt.answer {
				t.Fatalf("unexpected older_sister response: got %q want %q", got, tt.answer)
			}
			if len(executor.calls) != 1 {
				t.Fatalf("expected one tool call, got %d", len(executor.calls))
			}
			call := executor.calls[0]
			if got, want := call.Action, "older_sister"; got != want {
				t.Fatalf("unexpected routed action: got %q want %q", got, want)
			}
			var args map[string]any
			if err := json.Unmarshal(call.Args, &args); err != nil {
				t.Fatalf("unmarshal older_sister args: %v", err)
			}
			if got := strings.TrimSpace(fmt.Sprint(args["query"])); got != tt.question {
				t.Fatalf("unexpected older_sister query: got %q want %q", got, tt.question)
			}
		})
	}
}

func TestChatUsesRoutedPromptWhenCandidatesAvailable(t *testing.T) {
	t.Parallel()

	decisionCalls := 0
	server := testServer(t)
	server.cfg.DeterministicToolShortcuts = false
	server.readGrammar = func(path string) (string, error) {
		return `root ::= empty_list | single_call_list
tool_call ::= get_time_call | weather_call | older_sister_call | calculator_call | beemo_direct_call
calc_args ::= expression_args | convert_args | bmi_args | bmr_args | tdee_args | percent_of_args | percent_change_args | percent_ratio_args`, nil
	}
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
		decisionCalls++
		switch decisionCalls {
		case 1:
			if !strings.Contains(prompt, "Route decision:") {
				t.Fatalf("expected route decision prompt, got %q", prompt)
			}
			if !strings.Contains(prompt, "route_id: calculator.percent_of") {
				t.Fatalf("expected routed candidate id, got %q", prompt)
			}
			if !strings.Contains(prompt, "similarity: 0.910") {
				t.Fatalf("expected routed candidate similarity, got %q", prompt)
			}
			return `{"route_id":"calculator.percent_of"}`, nil
		case 2:
			if !strings.Contains(prompt, "Selected route:") {
				t.Fatalf("expected route extraction prompt, got %q", prompt)
			}
			if strings.Contains(grammar, "empty_list | single_call_list") {
				t.Fatalf("routed grammar should require one tool call: %q", grammar)
			}
			if !strings.Contains(grammar, "tool_call ::= calculator_call") {
				t.Fatalf("expected calculator-only grammar: %q", grammar)
			}
			if !strings.Contains(grammar, "calc_args ::= percent_of_args") {
				t.Fatalf("expected percent_of-only grammar: %q", grammar)
			}
			return `[{"tool":"calculator","args":{"operation":"percent_of","percent":20,"value":85}}]`, nil
		default:
			t.Fatalf("unexpected decision call %d prompt=%q", decisionCalls, prompt)
			return "", nil
		}
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
	if got, want := decisionCalls, 2; got != want {
		t.Fatalf("unexpected decision call count: got %d want %d", got, want)
	}
}

func TestChatMalformedRoutedDecisionDoesNotFallbackToTopRoute(t *testing.T) {
	t.Parallel()

	executor := &recordingExecutor{}
	server := testServer(t)
	server.tools = executor
	server.cfg.DeterministicToolShortcuts = false
	server.routeSelector = staticRouteSelector{
		candidates: []routing.Candidate{
			{
				Route: routing.Route{
					ID:      "calculator.percent_of",
					Handler: routing.Handler{Type: "tool", Target: "calculator"},
					DefaultArgs: map[string]any{
						"operation": "percent_of",
					},
				},
				Score: 0.93,
			},
		},
	}
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		return `{"tool":"calculator"`, nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		t.Fatalf("final LLM should not be called after malformed tool JSON")
		return "", nil
	}

	resp, err := server.Chat(context.Background(), chatRequest("calculate 20 percent of 85"))
	if err != nil {
		t.Fatalf("Chat returned transport error: %v", err)
	}
	if got := len(executor.calls); got != 0 {
		t.Fatalf("malformed routed decision should not execute top route, got %d calls", got)
	}
	if got, want := resp.GetStatus(), "error"; got != want {
		t.Fatalf("unexpected status: got %q want %q", got, want)
	}
	if got, want := resp.GetErrorKind(), "route_parse"; got != want {
		t.Fatalf("unexpected error kind: got %q want %q", got, want)
	}
}

func TestChatBeemoDirectSkipsToolExecution(t *testing.T) {
	t.Parallel()

	executor := &recordingExecutor{}
	server := testServer(t)
	server.cfg.DeterministicToolShortcuts = false
	server.tools = executor
	server.routeSelector = staticRouteSelector{
		candidates: []routing.Candidate{
			{
				Route: routing.Route{
					ID:      "beemo.direct",
					Title:   "Direct local response",
					Summary: "Answer directly with Beemo's local reasoning model.",
					Handler: routing.Handler{Type: "tool", Target: "beemo.direct"},
				},
				Score: 0.88,
			},
		},
	}
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		if !strings.Contains(prompt, "Route decision:") {
			t.Fatalf("expected route decision prompt, got %q", prompt)
		}
		if !strings.Contains(prompt, "similarity: 0.880") {
			t.Fatalf("expected routed candidate similarity, got %q", prompt)
		}
		return `{"route_id":"beemo.direct"}`, nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		if !strings.Contains(prompt, `"tool":"beemo.direct"`) {
			t.Fatalf("final prompt missing beemo.direct decision: %q", prompt)
		}
		return "I can help think through that.", nil
	}

	resp, err := server.Chat(context.Background(), chatRequest("can you help me think through an idea?"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "I can help think through that."; got != want {
		t.Fatalf("unexpected response text: got %q want %q", got, want)
	}
	if got := len(executor.calls); got != 0 {
		t.Fatalf("beemo.direct should not execute a tool, got %d calls", got)
	}
	if got := resp.GetTools(); len(got) != 1 || got[0] != "beemo.direct" {
		t.Fatalf("unexpected response tools: %#v", got)
	}
}

func TestChatVetoesHealthCalculatorRouteWithoutExplicitMetric(t *testing.T) {
	t.Parallel()

	executor := &recordingExecutor{}
	server := testServer(t)
	server.cfg.DeterministicToolShortcuts = false
	server.tools = executor
	server.routeSelector = staticRouteSelector{
		candidates: []routing.Candidate{
			{
				Route: routing.Route{
					ID:      "calculator.bmr",
					Domain:  "calculator",
					Handler: routing.Handler{Type: "tool", Target: "calculator"},
					DefaultArgs: map[string]any{
						"operation": "bmr",
					},
				},
				Score: 0.91,
			},
			{
				Route: routing.Route{
					ID:      "beemo.direct",
					Domain:  "beemo",
					Handler: routing.Handler{Type: "tool", Target: "beemo.direct"},
				},
				Score: 0.89,
			},
		},
	}
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		if !strings.Contains(prompt, "Route decision:") {
			t.Fatalf("expected route decision prompt, got %q", prompt)
		}
		return `{"route_id":"calculator.bmr"}`, nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		t.Fatalf("final LLM should not be called after route veto")
		return "", nil
	}

	resp, err := server.Chat(context.Background(), chatRequest("what is the burn time?"))
	if err != nil {
		t.Fatalf("Chat returned transport error: %v", err)
	}
	if got := len(executor.calls); got != 0 {
		t.Fatalf("vetoed route should not execute a tool, got %d calls", got)
	}
	if got, want := resp.GetStatus(), "error"; got != want {
		t.Fatalf("unexpected status: got %q want %q", got, want)
	}
	if got, want := resp.GetErrorKind(), "route_validation"; got != want {
		t.Fatalf("unexpected error kind: got %q want %q", got, want)
	}
}

func TestChatPendingCancelClearsWithoutRouting(t *testing.T) {
	t.Parallel()

	server := testServer(t)
	pending := pendingToolState{
		OriginalUserQuery: "what is my BMI?",
		Tool:              "calculator",
		Args:              []byte(`{"operation":"bmi"}`),
		Missing:           []string{"height"},
		Question:          "What is the height?",
	}
	server.setPending("test-session", pending)
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		t.Fatalf("LLM should not be called for pending cancellation: %q", prompt)
		return "", nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		t.Fatalf("final LLM should not be called for pending cancellation: %q", prompt)
		return "", nil
	}

	resp, err := server.Chat(context.Background(), chatRequest("cancelled"))
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got, want := resp.GetText(), "Canceled."; got != want {
		t.Fatalf("unexpected response text: got %q want %q", got, want)
	}
	if _, ok := server.getPending("test-session"); ok {
		t.Fatal("pending state should be cleared")
	}
}

func TestChatRoutesWithLatestUserQueryOnly(t *testing.T) {
	t.Parallel()

	selector := &queryRecordingRouteSelector{
		candidates: []routing.Candidate{
			{
				Route: routing.Route{
					ID:      "beemo.direct",
					Domain:  "beemo",
					Handler: routing.Handler{Type: "tool", Target: "beemo.direct"},
				},
				Score: 0.9,
			},
		},
	}
	server := testServer(t)
	server.cfg.DeterministicToolShortcuts = false
	server.routeSelector = selector
	server.callCompletion = func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
		return `{"route_id":"beemo.direct"}`, nil
	}
	server.callFinalMessage = func(httpURL, model, prompt string, timeout time.Duration) (string, error) {
		return "ok", nil
	}

	if _, err := server.Chat(context.Background(), sessionChatRequest("route-session", "what is 5 foot 6 inches in centimeters?")); err != nil {
		t.Fatalf("first Chat returned error: %v", err)
	}
	if _, err := server.Chat(context.Background(), sessionChatRequest("route-session", "what is the weather in Tokyo tomorrow?")); err != nil {
		t.Fatalf("second Chat returned error: %v", err)
	}

	if got, want := len(selector.queries), 2; got != want {
		t.Fatalf("unexpected route query count: got %d want %d", got, want)
	}
	if got, want := selector.queries[1], "what is the weather in Tokyo tomorrow?"; got != want {
		t.Fatalf("route retrieval should use latest query only: got %q want %q", got, want)
	}
	if strings.Contains(selector.queries[1], "5 foot 6") {
		t.Fatalf("route retrieval leaked prior turn: %q", selector.queries[1])
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

func mockNewYorkWeatherConfig(t *testing.T) orchtools.WeatherConfig {
	t.Helper()
	return orchtools.WeatherConfig{
		HTTPURL:           "https://forecast.test/v1/forecast",
		GeocodingURL:      "https://geocode.test/v1/search",
		TemperatureUnit:   "fahrenheit",
		WindSpeedUnit:     "mph",
		PrecipitationUnit: "inch",
		Now: func() time.Time {
			return time.Date(2026, 5, 17, 12, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
		},
		Fetch: func(ctx context.Context, requestURL string) ([]byte, error) {
			parsed, err := url.Parse(requestURL)
			if err != nil {
				return nil, err
			}
			switch parsed.Host {
			case "geocode.test":
				query := parsed.Query().Get("name")
				if query != "new york city" {
					return []byte(`{"results":[]}`), nil
				}
				return []byte(`{"results":[{"name":"New York","latitude":40.7128,"longitude":-74.0060,"timezone":"America/New_York","country":"United States"}]}`), nil
			case "forecast.test":
				values := parsed.Query()
				if got, want := values.Get("latitude"), "40.7128"; got != want {
					t.Fatalf("unexpected forecast latitude: got %q want %q", got, want)
				}
				if got, want := values.Get("longitude"), "-74.006"; got != want {
					t.Fatalf("unexpected forecast longitude: got %q want %q", got, want)
				}
				return []byte(`{
					"timezone":"America/New_York",
					"current_units":{"temperature_2m":"°F"},
					"current":{"time":"2026-05-17T12:00","temperature_2m":70.0,"weather_code":1},
					"hourly_units":{"temperature_2m":"°F","precipitation_probability":"%"},
					"hourly":{
						"time":["2026-05-17T18:00","2026-05-18T06:00"],
						"temperature_2m":[66.0,58.0],
						"precipitation_probability":[15,5],
						"weather_code":[1,0]
					},
					"daily_units":{"temperature_2m_max":"°F","temperature_2m_min":"°F","precipitation_probability_max":"%","precipitation_sum":"in"},
					"daily":{
						"time":["2026-05-17","2026-05-18"],
						"weather_code":[1,0],
						"temperature_2m_max":[70.0,75.0],
						"temperature_2m_min":[60.0,62.0],
						"precipitation_probability_max":[20,10],
						"precipitation_sum":[0.05,0.00]
					}
				}`), nil
			default:
				t.Fatalf("unexpected weather fetch host %q from %s", parsed.Host, requestURL)
				return nil, nil
			}
		},
	}
}

func TestParseToolCallsRejectsNonObjectArgs(t *testing.T) {
	t.Parallel()

	_, err := parseToolCalls(`[{"tool":"calculator","args":[]}]`)
	if err == nil {
		t.Fatal("expected non-object args to be rejected")
	}
	if !strings.Contains(err.Error(), "tool args must be a JSON object") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResumePendingDecisionClearsInterruptedTool(t *testing.T) {
	t.Parallel()

	server := testServer(t)
	pending := pendingToolState{
		OriginalUserQuery: "what is my BMI?",
		Tool:              "calculator",
		Args:              []byte(`{"operation":"bmi"}`),
		Missing:           []string{"height"},
		Question:          "What is the height?",
	}
	server.setPending("test-session", pending)

	decision, handled, cleared, err := server.resumePendingDecision(pendingResumeRequest{
		sessionID:        "test-session",
		pending:          pending,
		userQuery:        "what time is it?",
		activeTranscript: "",
		grammar:          "root ::= \"[]\"",
		callTimeout:      time.Second,
		callCompletion: func(httpURL, model, prompt, grammar string, timeout time.Duration) (string, error) {
			t.Fatal("LLM should not be called when deterministic inference interrupts pending tool")
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("resumePendingDecision returned error: %v", err)
	}
	if !handled || !cleared {
		t.Fatalf("expected handled and cleared; handled=%v cleared=%v", handled, cleared)
	}
	if !strings.Contains(decision.Text, `"tool":"get_time"`) {
		t.Fatalf("unexpected decision text: %s", decision.Text)
	}
	if _, ok := server.getPending("test-session"); ok {
		t.Fatal("pending state should be cleared")
	}
}

func testServer(t *testing.T) *orchestratorServer {
	t.Helper()
	return &orchestratorServer{
		cfg: config.Config{
			LLMHTTPURL:                 "http://llm.test/v1/chat/completions",
			LLMModel:                   "test-model",
			LLMTimeoutMs:               500,
			EmbeddingTimeoutMs:         500,
			DeterministicToolShortcuts: true,
			DirectToolResponses:        true,
		},
		tools: orchtools.NewLocalExecutor(),
		readGrammar: func(path string) (string, error) {
			return "root ::= \"[]\"", nil
		},
	}
}

func chatRequest(text string) *pb.ChatRequest {
	return sessionChatRequest("test-session", text)
}

func sessionChatRequest(sessionID, text string) *pb.ChatRequest {
	return &pb.ChatRequest{
		SessionId: sessionID,
		Messages: []*pb.ChatMessage{
			{Role: "user", Content: text},
		},
	}
}
