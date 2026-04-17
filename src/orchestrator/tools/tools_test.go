package tools

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestCalculatorSuccessfulOperations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		args   map[string]any
		output string
	}{
		{
			name: "convert_height_ft_in_to_cm",
			args: map[string]any{
				"operation": "convert",
				"input": []map[string]any{
					{"unit": "ft", "value": 5},
					{"unit": "in", "value": 4},
				},
				"to_unit": "cm",
			},
			output: "5 ft 4 in = 162.56 cm",
		},
		{
			name: "convert_weight_lb_to_g",
			args: map[string]any{
				"operation": "convert",
				"input": []map[string]any{
					{"unit": "lb", "value": 101},
				},
				"to_unit": "g",
			},
			output: "101 lb = 45812.82937 g",
		},
		{
			name: "convert_distance_over_time_to_pace",
			args: map[string]any{
				"operation": "convert",
				"input": []map[string]any{
					{"unit": "mi", "value": 5},
				},
				"per": []map[string]any{
					{"unit": "hr", "value": 1},
				},
				"to_unit": "min/mi",
			},
			output: "5 mi / 1 hr = 12 min/mi",
		},
		{
			name: "convert_rate_expression_to_pace",
			args: map[string]any{
				"operation": "convert",
				"value":     10,
				"from_unit": "mi/hr",
				"to_unit":   "min/mi",
			},
			output: "10 mi/hr = 6 min/mi",
		},
		{
			name: "convert_chemistry_rate",
			args: map[string]any{
				"operation": "convert",
				"value":     5,
				"from_unit": "mg/ml",
				"to_unit":   "g/l",
			},
			output: "5 mg/ml = 5 g/l",
		},
		{
			name: "bmi_metric",
			args: map[string]any{
				"operation": "bmi",
				"weight": []map[string]any{
					{"unit": "kg", "value": 45},
				},
				"height": []map[string]any{
					{"unit": "in", "value": 64},
				},
			},
			output: "BMI 17.03",
		},
		{
			name: "bmr",
			args: map[string]any{
				"operation": "bmr",
				"age_years": 34,
				"gender":    "female",
				"weight": []map[string]any{
					{"unit": "kg", "value": 45},
				},
				"height": []map[string]any{
					{"unit": "in", "value": 64},
				},
			},
			output: "BMR 1135.00 kcal/day",
		},
		{
			name: "bmr_sanitizes_mixed_weight_and_height_fields",
			args: map[string]any{
				"operation": "bmr",
				"age_years": 27,
				"gender":    "female",
				"weight": []map[string]any{
					{"unit": "lb", "value": 134},
					{"unit": "cm", "value": 172},
				},
				"height": []map[string]any{
					{"unit": "lb", "value": 134},
					{"unit": "cm", "value": 172},
				},
			},
			output: "BMR 1386.81 kcal/day",
		},
		{
			name: "percent_of",
			args: map[string]any{
				"operation": "percent_of",
				"percent":   20,
				"value":     85,
			},
			output: "20% of 85 = 17",
		},
		{
			name: "percent_change",
			args: map[string]any{
				"operation": "percent_change",
				"value":     85,
				"percent":   12,
				"direction": "increase",
			},
			output: "85 increased by 12% = 95.2",
		},
		{
			name: "percent_ratio",
			args: map[string]any{
				"operation": "percent_ratio",
				"part":      18,
				"whole":     24,
			},
			output: "18 is 75% of 24",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := mustExecuteCalculator(t, tt.args)
			if result.Status != "" {
				t.Fatalf("expected normal result, got status %q", result.Status)
			}
			if result.Output != tt.output {
				t.Fatalf("unexpected output: %q", result.Output)
			}
		})
	}
}

func TestCalculatorNeedsInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     map[string]any
		status   string
		missing  []string
		question string
	}{
		{
			name: "bmi_missing_height",
			args: map[string]any{
				"operation": "bmi",
				"weight": []map[string]any{
					{"unit": "kg", "value": 45},
				},
			},
			status:   "needs_input",
			missing:  []string{"height"},
			question: "What is the height?",
		},
		{
			name: "bmi_zero_weight_treated_as_missing",
			args: map[string]any{
				"operation": "bmi",
				"weight": []map[string]any{
					{"unit": "kg", "value": 0},
				},
				"height": []map[string]any{
					{"unit": "in", "value": 64},
				},
			},
			status:   "needs_input",
			missing:  []string{"weight"},
			question: "What is the weight?",
		},
		{
			name: "tdee_missing_activity_level",
			args: map[string]any{
				"operation": "tdee",
				"age_years": 34,
				"gender":    "female",
				"weight": []map[string]any{
					{"unit": "kg", "value": 45},
				},
				"height": []map[string]any{
					{"unit": "in", "value": 64},
				},
			},
			status:   "needs_input",
			missing:  []string{"activity_level"},
			question: "What is the activity level: sedentary, light, moderate, active, or very_active?",
		},
		{
			name: "tdee_missing_age_gender",
			args: map[string]any{
				"operation": "tdee",
				"weight": []map[string]any{
					{"unit": "lb", "value": 134},
				},
				"height": []map[string]any{
					{"unit": "cm", "value": 174},
				},
			},
			status:   "needs_input",
			missing:  []string{"age_years", "gender"},
			question: "What are the age in years and gender?",
		},
		{
			name: "bmr_mixed_height_field_without_length_becomes_missing",
			args: map[string]any{
				"operation": "bmr",
				"age_years": 27,
				"gender":    "female",
				"weight": []map[string]any{
					{"unit": "lb", "value": 134},
				},
				"height": []map[string]any{
					{"unit": "lb", "value": 134},
				},
			},
			status:   "needs_input",
			missing:  []string{"height"},
			question: "What is the height?",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := mustExecuteCalculator(t, tt.args)
			if result.Status != tt.status {
				t.Fatalf("expected status %q, got %q", tt.status, result.Status)
			}
			if result.Question != tt.question {
				t.Fatalf("unexpected question: %q", result.Question)
			}
			if !equalStrings(result.Missing, tt.missing) {
				t.Fatalf("unexpected missing fields: got %#v want %#v", result.Missing, tt.missing)
			}
		})
	}
}

func TestGetTimeToolReturnsRFC3339Timestamp(t *testing.T) {
	t.Parallel()

	exec := NewLocalExecutor()
	result, err := exec.Execute(context.Background(), Request{Action: "get_time"})
	if err != nil {
		t.Fatalf("execute get_time: %v", err)
	}
	if result.Action != "get_time" {
		t.Fatalf("unexpected action: %q", result.Action)
	}
	if result.Status != "" {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if result.Output == "" {
		t.Fatal("expected non-empty timestamp output")
	}
	if _, err := time.Parse(time.RFC3339, result.Output); err != nil {
		t.Fatalf("output is not RFC3339: %q err=%v", result.Output, err)
	}
}

func TestGetTimeToolReturnsCurrentTimestamp(t *testing.T) {
	t.Parallel()

	before := time.Now().Add(-2 * time.Second)
	exec := NewLocalExecutor()
	result, err := exec.Execute(context.Background(), Request{Action: "get_time"})
	if err != nil {
		t.Fatalf("execute get_time: %v", err)
	}
	after := time.Now().Add(2 * time.Second)

	got, err := time.Parse(time.RFC3339, result.Output)
	if err != nil {
		t.Fatalf("parse timestamp: %v", err)
	}
	if got.Before(before) || got.After(after) {
		t.Fatalf("timestamp out of expected range: got=%s before=%s after=%s", got.Format(time.RFC3339), before.Format(time.RFC3339), after.Format(time.RFC3339))
	}
}

func TestGetTimeToolIgnoresArgs(t *testing.T) {
	t.Parallel()

	exec := NewLocalExecutor()
	result, err := exec.Execute(context.Background(), Request{
		Action: "get_time",
		Args:   json.RawMessage(`{"unused":"value"}`),
	})
	if err != nil {
		t.Fatalf("execute get_time with args: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, result.Output); err != nil {
		t.Fatalf("output is not RFC3339: %q err=%v", result.Output, err)
	}
}

func TestResolveCalculatorCallCanonicalizesDuplicateEquivalentMeasurements(t *testing.T) {
	t.Parallel()

	resolved, err := ResolveCalculatorCall(PlannedCall{
		Action: "calculator",
		Args: json.RawMessage(`{
			"operation":"bmr",
			"weight":[{"unit":"lb","value":134},{"unit":"kg","value":60.88}],
			"height":[{"unit":"cm","value":174},{"unit":"m","value":1.74}],
			"age_years":27,
			"gender":"female"
		}`),
	}, "", nil)
	if err != nil {
		t.Fatalf("ResolveCalculatorCall returned error: %v", err)
	}

	var args struct {
		Operation string                 `json:"operation"`
		Weight    []measurementComponent `json:"weight"`
		Height    []measurementComponent `json:"height"`
		AgeYears  float64                `json:"age_years"`
		Gender    string                 `json:"gender"`
	}
	if err := json.Unmarshal(resolved.Args, &args); err != nil {
		t.Fatalf("unmarshal resolved args: %v", err)
	}

	if got, want := args.Operation, "bmr"; got != want {
		t.Fatalf("unexpected operation: got %q want %q", got, want)
	}
	if len(args.Weight) != 1 || args.Weight[0].Unit != "kg" {
		t.Fatalf("unexpected canonicalized weight: %#v", args.Weight)
	}
	if diff := math.Abs(args.Weight[0].Value - 60.78137758); diff > 0.000001 {
		t.Fatalf("unexpected canonicalized weight value: got %.8f", args.Weight[0].Value)
	}
	if len(args.Height) != 1 || args.Height[0].Unit != "cm" || args.Height[0].Value != 174 {
		t.Fatalf("unexpected canonicalized height: %#v", args.Height)
	}
	if got, want := args.AgeYears, 27.0; got != want {
		t.Fatalf("unexpected age_years: got %v want %v", got, want)
	}
	if got, want := args.Gender, "female"; got != want {
		t.Fatalf("unexpected gender: got %q want %q", got, want)
	}
}

func TestResolveCalculatorCallPrefersSnapshotOverModelFallback(t *testing.T) {
	t.Parallel()

	resolved, err := ResolveCalculatorCall(
		PlannedCall{
			Action: "calculator",
			Args: json.RawMessage(`{
				"operation":"bmr",
				"weight":[{"unit":"kg","value":134}],
				"height":[{"unit":"lb","value":134}],
				"age_years":27,
				"gender":"female"
			}`),
		},
		"what is her bmr? she is female and 27 years old",
		map[string]json.RawMessage{
			"weight": json.RawMessage(`[{"unit":"kg","value":60.78137758}]`),
			"height": json.RawMessage(`[{"unit":"cm","value":174}]`),
		},
	)
	if err != nil {
		t.Fatalf("ResolveCalculatorCall returned error: %v", err)
	}

	var args struct {
		Weight   []measurementComponent `json:"weight"`
		Height   []measurementComponent `json:"height"`
		AgeYears float64                `json:"age_years"`
		Gender   string                 `json:"gender"`
	}
	if err := json.Unmarshal(resolved.Args, &args); err != nil {
		t.Fatalf("unmarshal resolved args: %v", err)
	}

	if len(args.Weight) != 1 || args.Weight[0].Unit != "kg" {
		t.Fatalf("unexpected weight: %#v", args.Weight)
	}
	if diff := math.Abs(args.Weight[0].Value - 60.78137758); diff > 0.000001 {
		t.Fatalf("unexpected weight value: got %.8f", args.Weight[0].Value)
	}
	if len(args.Height) != 1 || args.Height[0].Unit != "cm" || args.Height[0].Value != 174 {
		t.Fatalf("unexpected height: %#v", args.Height)
	}
	if got, want := args.AgeYears, 27.0; got != want {
		t.Fatalf("unexpected age_years: got %v want %v", got, want)
	}
	if got, want := args.Gender, "female"; got != want {
		t.Fatalf("unexpected gender: got %q want %q", got, want)
	}
}

func mustExecuteCalculator(t *testing.T, args map[string]any) Result {
	t.Helper()

	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	exec := NewLocalExecutor()
	result, err := exec.Execute(context.Background(), Request{
		Action: "calculator",
		Args:   raw,
	})
	if err != nil {
		t.Fatalf("execute calculator: %v", err)
	}
	return result
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
