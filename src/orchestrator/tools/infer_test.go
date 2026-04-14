package tools

import (
	"encoding/json"
	"testing"
)

func TestTryFillPendingParsesCombinedAgeGenderAndHeightReply(t *testing.T) {
	t.Parallel()

	call, ok, err := TryFillPending(PendingFillRequest{
		Action:  "calculator",
		Args:    json.RawMessage(`{"operation":"bmr","weight":[{"unit":"kg","value":45}]}`),
		Missing: []string{"age_years", "gender", "height"},
		Reply:   "34 years, female, 162cm",
	})
	if err != nil {
		t.Fatalf("TryFillPending returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected TryFillPending to parse the combined reply")
	}

	var args struct {
		Operation string                 `json:"operation"`
		AgeYears  float64                `json:"age_years"`
		Gender    string                 `json:"gender"`
		Height    []measurementComponent `json:"height"`
		Weight    []measurementComponent `json:"weight"`
	}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		t.Fatalf("unmarshal merged args: %v", err)
	}

	if got, want := args.Operation, "bmr"; got != want {
		t.Fatalf("unexpected operation: got %q want %q", got, want)
	}
	if got, want := args.AgeYears, 34.0; got != want {
		t.Fatalf("unexpected age_years: got %v want %v", got, want)
	}
	if got, want := args.Gender, "female"; got != want {
		t.Fatalf("unexpected gender: got %q want %q", got, want)
	}
	if len(args.Height) != 1 || args.Height[0].Unit != "cm" || args.Height[0].Value != 162 {
		t.Fatalf("unexpected height: %#v", args.Height)
	}
	if len(args.Weight) != 1 || args.Weight[0].Unit != "kg" || args.Weight[0].Value != 45 {
		t.Fatalf("unexpected preserved weight: %#v", args.Weight)
	}
}

func TestGroundCallKeepsCentimetersSpelledOut(t *testing.T) {
	t.Parallel()

	call, err := GroundCall(
		"what is the BMI of 45 kilograms and 162 centimeters?",
		PlannedCall{
			Action: "calculator",
			Args: json.RawMessage(`{
				"operation":"bmi",
				"weight":[{"unit":"kg","value":45}],
				"height":[{"unit":"cm","value":162}]
			}`),
		},
	)
	if err != nil {
		t.Fatalf("GroundCall returned error: %v", err)
	}

	var args struct {
		Height []measurementComponent `json:"height"`
		Weight []measurementComponent `json:"weight"`
	}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		t.Fatalf("unmarshal grounded args: %v", err)
	}

	if len(args.Weight) != 1 || args.Weight[0].Unit != "kg" || args.Weight[0].Value != 45 {
		t.Fatalf("unexpected grounded weight: %#v", args.Weight)
	}
	if len(args.Height) != 1 || args.Height[0].Unit != "cm" || args.Height[0].Value != 162 {
		t.Fatalf("unexpected grounded height: %#v", args.Height)
	}
}
