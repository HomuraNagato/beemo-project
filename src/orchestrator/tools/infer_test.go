package tools

import (
	"encoding/json"
	"strings"
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

func TestTryFillPendingParsesAgeIsReply(t *testing.T) {
	t.Parallel()

	call, ok, err := TryFillPending(PendingFillRequest{
		Action:  "calculator",
		Args:    json.RawMessage(`{"operation":"tdee","weight":[{"unit":"lb","value":134}],"height":[{"unit":"cm","value":174}]}`),
		Missing: []string{"age_years", "gender"},
		Reply:   "her gender is female and her age is 27",
	})
	if err != nil {
		t.Fatalf("TryFillPending returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected TryFillPending to parse the age is reply")
	}

	var args struct {
		Operation string  `json:"operation"`
		AgeYears  float64 `json:"age_years"`
		Gender    string  `json:"gender"`
	}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		t.Fatalf("unmarshal merged args: %v", err)
	}
	if got, want := args.Operation, "tdee"; got != want {
		t.Fatalf("unexpected operation: got %q want %q", got, want)
	}
	if got, want := args.AgeYears, 27.0; got != want {
		t.Fatalf("unexpected age_years: got %v want %v", got, want)
	}
	if got, want := args.Gender, "female"; got != want {
		t.Fatalf("unexpected gender: got %q want %q", got, want)
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

func TestInferToolCallForObviousTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		text       string
		wantAction string
		wantArg    string
	}{
		{
			name:       "time",
			text:       "what time is it?",
			wantAction: "get_time",
		},
		{
			name:       "weather",
			text:       "will it rain tomorrow?",
			wantAction: "weather",
			wantArg:    `"focus":"rain"`,
		},
		{
			name:       "older sister",
			text:       "ask older sister to search for current vllm docs",
			wantAction: "older_sister",
			wantArg:    `"web_search":true`,
		},
		{
			name:       "memory",
			text:       "when is my birthday?",
			wantAction: "memory_lookup",
			wantArg:    `"attribute":"birthday"`,
		},
		{
			name:       "percent",
			text:       "what is 20% of 85?",
			wantAction: "calculator",
			wantArg:    `"operation":"percent_of"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			call, ok, err := InferToolCall(tt.text)
			if err != nil {
				t.Fatalf("InferToolCall returned error: %v", err)
			}
			if !ok {
				t.Fatal("expected inferred tool call")
			}
			if call.Action != tt.wantAction {
				t.Fatalf("unexpected action: got %q want %q", call.Action, tt.wantAction)
			}
			if tt.wantArg != "" && !jsonContains(call.Args, tt.wantArg) {
				t.Fatalf("expected args to contain %s in %s", tt.wantArg, call.Args)
			}
		})
	}
}

func TestExtractObservationPatchIncludesTextFacts(t *testing.T) {
	t.Parallel()

	patch, ok, err := ExtractObservationPatch("my birthday is June 4")
	if err != nil {
		t.Fatalf("ExtractObservationPatch returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected observation patch")
	}
	if !jsonContains(patch, `"birthday":"June 4"`) {
		t.Fatalf("expected birthday in patch: %s", patch)
	}
}

func TestExtractObservationPatchIncludesGenericTextFacts(t *testing.T) {
	t.Parallel()

	patch, ok, err := ExtractObservationPatch("my favorite food is mango rice and my comfort show is Adventure Time")
	if err != nil {
		t.Fatalf("ExtractObservationPatch returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected observation patch")
	}
	for _, fragment := range []string{
		`"favorite_food":"mango rice"`,
		`"comfort_show":"Adventure Time"`,
	} {
		if !jsonContains(patch, fragment) {
			t.Fatalf("expected patch to contain %s in %s", fragment, patch)
		}
	}
}

func TestExtractObservationPatchIncludesGenericTextFactVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		want string
	}{
		{text: "please remember my codename is Moonrise", want: `"codename":"Moonrise"`},
		{text: "update my codename to Sunrise", want: `"codename":"Sunrise"`},
		{text: "set my project motto to steady sparks", want: `"project_motto":"steady sparks"`},
		{text: "change my detail 042 to corrected-042 from now on", want: `"detail_042":"corrected-042"`},
		{text: "my detail 043 should be corrected-043", want: `"detail_043":"corrected-043"`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.text, func(t *testing.T) {
			t.Parallel()

			patch, ok, err := ExtractObservationPatch(tt.text)
			if err != nil {
				t.Fatalf("ExtractObservationPatch returned error: %v", err)
			}
			if !ok {
				t.Fatal("expected observation patch")
			}
			if !jsonContains(patch, tt.want) {
				t.Fatalf("expected patch to contain %s in %s", tt.want, patch)
			}
		})
	}
}

func TestInferToolCallForGenericMemoryLookup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text    string
		wantArg string
	}{
		{text: "what is my favorite food?", wantArg: `"attribute":"favorite_food"`},
		{text: "what was my favorite food again?", wantArg: `"attribute":"favorite_food"`},
		{text: "can you remind me what my codename is?", wantArg: `"attribute":"codename"`},
		{text: "do you know my project motto?", wantArg: `"attribute":"project_motto"`},
		{text: "what do you remember about my comfort show?", wantArg: `"attribute":"comfort_show"`},
		{text: "what did I say my detail 042 was?", wantArg: `"attribute":"detail_042"`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.text, func(t *testing.T) {
			t.Parallel()

			call, ok, err := InferToolCall(tt.text)
			if err != nil {
				t.Fatalf("InferToolCall returned error: %v", err)
			}
			if !ok {
				t.Fatal("expected inferred tool call")
			}
			if got, want := call.Action, "memory_lookup"; got != want {
				t.Fatalf("unexpected action: got %q want %q", got, want)
			}
			if !jsonContains(call.Args, tt.wantArg) {
				t.Fatalf("expected lookup args to contain %s in %s", tt.wantArg, call.Args)
			}
		})
	}

	call, ok, err := InferToolCall("what is my girlfriend BMI?")
	if err != nil {
		t.Fatalf("InferToolCall returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected inferred calculator call")
	}
	if got, want := call.Action, "calculator"; got != want {
		t.Fatalf("unexpected action for BMI request: got %q want %q", got, want)
	}
}

func TestExtractObservationPatchDoesNotInferGenderFromMango(t *testing.T) {
	t.Parallel()

	patch, ok, err := ExtractObservationPatch("my favorite food is mango rice")
	if err != nil {
		t.Fatalf("ExtractObservationPatch returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected observation patch")
	}
	if jsonContains(patch, `"gender"`) {
		t.Fatalf("did not expect gender in patch: %s", patch)
	}
	if !jsonContains(patch, `"favorite_food":"mango rice"`) {
		t.Fatalf("expected favorite_food in patch: %s", patch)
	}
}

func jsonContains(raw json.RawMessage, fragment string) bool {
	return strings.Contains(string(raw), fragment)
}
