package routing

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSelectorRetrieveRanksBestRouteAndFormatsCandidates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "routes.yaml")
	if err := os.WriteFile(path, []byte(`
domains:
  - id: time
    title: Time and date
    summary: Handle current and relative time and date questions.
    example_requests:
      - what time is it?
      - what day is tomorrow?
  - id: calculator
    title: Calculator
    summary: Handle arithmetic, percentages, conversions, and health calculations.
    example_requests:
      - what is 20% of 85?
routes:
  - id: get_time.current_or_relative
    domain: time
    title: Current or relative date and time
    summary: Answer questions about the current time, date, day, month, year, or relative dates.
    when_to_use:
      - Use for time and date questions.
    example_requests:
      - what time is it?
      - what day is tomorrow?
    handler:
      type: tool
      target: get_time
    default_args: {}
  - id: calculator.percent_of
    domain: calculator
    title: Percentage of a value
    summary: Calculate what percentage of a number is.
    when_to_use:
      - Use when the user asks for percent of a value.
    example_requests:
      - what is 20% of 85?
    handler:
      type: tool
      target: calculator
    default_args:
      operation: percent_of
  - id: calculator.bmi
    domain: calculator
    title: Body mass index
    summary: Calculate BMI from weight and height.
    handler:
      type: tool
      target: calculator
    default_args:
      operation: bmi
    memory:
      read: true
      write: true
      attrs:
        - weight
        - height
      scope: subject
`), 0644); err != nil {
		t.Fatalf("write routes file: %v", err)
	}

	selector := NewSelector(path, "http://embed.test/v1/embeddings", "test-model", 2, 1)
	selector.embedFn = func(httpURL, model string, inputs []string, timeout time.Duration) ([][]float32, error) {
		var vectors [][]float32
		for _, input := range inputs {
			switch {
			case strings.Contains(input, "Domain: time"):
				vectors = append(vectors, []float32{1, 0})
			case strings.Contains(input, "Domain: calculator"):
				vectors = append(vectors, []float32{0, 1})
			case strings.Contains(input, "what time is it?"):
				vectors = append(vectors, []float32{1, 0})
			case strings.Contains(input, "what day is tomorrow?"):
				vectors = append(vectors, []float32{1, 0})
			case strings.Contains(input, "20% of 85"):
				vectors = append(vectors, []float32{0, 1})
			case strings.Contains(input, "Current or relative"):
				vectors = append(vectors, []float32{1, 0})
			case strings.Contains(input, "Percentage of a value"):
				vectors = append(vectors, []float32{0, 1})
			case strings.Contains(input, "retrieve the best assistant execution pathway"):
				vectors = append(vectors, []float32{0.95, 0.05})
			default:
				vectors = append(vectors, []float32{0.5, 0.5})
			}
		}
		return vectors, nil
	}

	candidates, err := selector.Retrieve("what time is it right now?", time.Second)
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if got, want := len(candidates), 1; got != want {
		t.Fatalf("unexpected candidate count: got %d want %d", got, want)
	}
	if got, want := candidates[0].Route.ID, "get_time.current_or_relative"; got != want {
		t.Fatalf("unexpected top route: got %q want %q", got, want)
	}

	block := FormatCandidates(candidates)
	if !strings.Contains(block, "route_id: get_time.current_or_relative") {
		t.Fatalf("candidate block missing route id: %q", block)
	}
	if !strings.Contains(block, "domain_id: time") {
		t.Fatalf("candidate block missing domain id: %q", block)
	}
}

func TestMatchCallUsesRouteMemoryPolicy(t *testing.T) {
	t.Parallel()

	candidates := []Candidate{
		{
			Route: Route{
				ID:      "calculator.bmr",
				Domain:  "calculator",
				Handler: Handler{Type: "tool", Target: "calculator"},
				DefaultArgs: map[string]any{
					"operation": "bmr",
				},
				Memory: MemoryPolicy{
					Read:  true,
					Write: true,
					Attrs: []string{"weight", "height", "age_years", "gender"},
					Scope: "subject",
				},
			},
		},
	}

	route, ok, err := MatchCall(candidates, nil, "calculator", json.RawMessage(`{"operation":"bmr","age_years":27}`))
	if err != nil {
		t.Fatalf("MatchCall returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected MatchCall to match calculator.bmr")
	}
	if got, want := route.ID, "calculator.bmr"; got != want {
		t.Fatalf("unexpected route id: got %q want %q", got, want)
	}
	if !route.Memory.Read || !route.Memory.Write {
		t.Fatalf("unexpected memory policy: %#v", route.Memory)
	}
	if got, want := strings.Join(route.Memory.Attrs, ","), "weight,height,age_years,gender"; got != want {
		t.Fatalf("unexpected memory attrs: got %q want %q", got, want)
	}
}

func TestSelectorWarmupPreloadsDomainAndRouteEmbeddings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "routes.yaml")
	if err := os.WriteFile(path, []byte(`
domains:
  - id: time
    title: Time and date
    summary: Handle time requests.
    example_requests:
      - what time is it?
routes:
  - id: get_time.current_or_relative
    domain: time
    title: Current or relative date and time
    summary: Answer time and date questions.
    example_requests:
      - what day is tomorrow?
    handler:
      type: tool
      target: get_time
    default_args: {}
`), 0644); err != nil {
		t.Fatalf("write routes file: %v", err)
	}

	selector := NewSelector(path, "http://embed.test/v1/embeddings", "test-model", 2, 1)
	callCount := 0
	selector.embedFn = func(httpURL, model string, inputs []string, timeout time.Duration) ([][]float32, error) {
		callCount++
		vectors := make([][]float32, 0, len(inputs))
		for _, input := range inputs {
			switch {
			case input == "startup probe":
				vectors = append(vectors, []float32{0.1, 0.1})
			case strings.Contains(input, "retrieve the best assistant execution pathway"):
				vectors = append(vectors, []float32{1, 0})
			case strings.Contains(input, "Domain: time"):
				vectors = append(vectors, []float32{1, 0})
			case strings.Contains(input, "Route: get_time.current_or_relative"):
				vectors = append(vectors, []float32{1, 0})
			default:
				vectors = append(vectors, []float32{0.5, 0.5})
			}
		}
		return vectors, nil
	}

	if err := selector.Warmup(time.Second); err != nil {
		t.Fatalf("Warmup returned error: %v", err)
	}
	if got, want := callCount, 3; got != want {
		t.Fatalf("unexpected warmup call count: got %d want %d", got, want)
	}
	if got, want := len(selector.domains), 1; got != want {
		t.Fatalf("unexpected domain cache count: got %d want %d", got, want)
	}
	if got, want := len(selector.routes), 1; got != want {
		t.Fatalf("unexpected route cache count: got %d want %d", got, want)
	}
	if got, want := len(selector.domainEmbeddings), len(selector.domainDocuments); got != want {
		t.Fatalf("unexpected domain embedding cache: got %d want %d", got, want)
	}
	if got, want := len(selector.routeEmbeddings), len(selector.routeDocuments); got != want {
		t.Fatalf("unexpected route embedding cache: got %d want %d", got, want)
	}

	candidates, err := selector.Retrieve("what time is it right now?", time.Second)
	if err != nil {
		t.Fatalf("Retrieve returned error: %v", err)
	}
	if got, want := callCount, 4; got != want {
		t.Fatalf("expected cached retrieve to add only one query embedding call, got %d", got)
	}
	if got, want := len(candidates), 1; got != want {
		t.Fatalf("unexpected candidate count: got %d want %d", got, want)
	}
}

func TestSelectorWarmupFailsWhenEmbeddingProbeFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "routes.yaml")
	if err := os.WriteFile(path, []byte(`
domains:
  - id: time
    title: Time and date
    summary: Handle time requests.
routes:
  - id: get_time.current_or_relative
    domain: time
    title: Current or relative date and time
    summary: Answer time and date questions.
    handler:
      type: tool
      target: get_time
`), 0644); err != nil {
		t.Fatalf("write routes file: %v", err)
	}

	selector := NewSelector(path, "http://embed.test/v1/embeddings", "test-model", 2, 1)
	selector.embedFn = func(httpURL, model string, inputs []string, timeout time.Duration) ([][]float32, error) {
		return nil, fmt.Errorf("embedding offline")
	}

	err := selector.Warmup(time.Second)
	if err == nil {
		t.Fatal("expected warmup error, got nil")
	}
	if !strings.Contains(err.Error(), "embedding startup probe failed") {
		t.Fatalf("unexpected warmup error: %v", err)
	}
}
