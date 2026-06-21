package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
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

func TestWeatherOperations(t *testing.T) {
	t.Parallel()

	exec := NewLocalExecutorWithWeather(WeatherConfig{
		HTTPURL:           "https://api.open-meteo.com/v1/forecast",
		Latitude:          "40.7128",
		Longitude:         "-74.0060",
		Timezone:          "America/New_York",
		LocationName:      "New York",
		TemperatureUnit:   "fahrenheit",
		WindSpeedUnit:     "mph",
		PrecipitationUnit: "inch",
		Now: func() time.Time {
			return time.Date(2026, 4, 23, 12, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
		},
		Fetch: func(ctx context.Context, requestURL string) ([]byte, error) {
			return []byte(`{
				"timezone":"America/New_York",
				"current_units":{"temperature_2m":"°F"},
				"current":{"time":"2026-04-23T12:00","temperature_2m":71.6,"weather_code":2},
				"hourly_units":{"temperature_2m":"°F","precipitation_probability":"%"},
				"hourly":{
					"time":["2026-04-23T17:00","2026-04-23T18:00","2026-04-23T19:00","2026-04-24T06:00"],
					"temperature_2m":[68.2,66.0,64.5,57.3],
					"precipitation_probability":[15,25,35,45],
					"weather_code":[2,3,61,3]
				},
				"daily_units":{"temperature_2m_max":"°F","temperature_2m_min":"°F","precipitation_probability_max":"%","precipitation_sum":"in"},
				"daily":{
					"time":["2026-04-23","2026-04-24"],
					"weather_code":[3,61],
					"temperature_2m_max":[73.4,69.1],
					"temperature_2m_min":[58.2,55.0],
					"precipitation_probability_max":[20,65],
					"precipitation_sum":[0.02,0.31]
				}
			}`), nil
		},
	})

	tests := []struct {
		name   string
		args   map[string]any
		output string
	}{
		{
			name:   "today_general",
			args:   map[string]any{"when": "today", "focus": "general", "location_name": "New York", "latitude": "40.7128", "longitude": "-74.0060", "timezone": "America/New_York"},
			output: "Today in New York: overcast skies, high 73.4°F, low 58.2°F, rain chance up to 20%.",
		},
		{
			name:   "current_temperature",
			args:   map[string]any{"when": "current", "focus": "temperature", "location_name": "New York", "latitude": "40.7128", "longitude": "-74.0060", "timezone": "America/New_York"},
			output: "Current temperature in New York: 71.6°F.",
		},
		{
			name:   "today_rain",
			args:   map[string]any{"when": "today", "focus": "rain", "location_name": "New York", "latitude": "40.7128", "longitude": "-74.0060", "timezone": "America/New_York"},
			output: "Rain looks unlikely in New York today. Chance up to 20% with about 0.02 in expected.",
		},
		{
			name:   "tomorrow_general",
			args:   map[string]any{"when": "tomorrow", "focus": "general", "location_name": "New York", "latitude": "40.7128", "longitude": "-74.0060", "timezone": "America/New_York"},
			output: "Tomorrow in New York: rain, high 69.1°F, low 55°F, rain chance up to 65%.",
		},
		{
			name:   "evening_general",
			args:   map[string]any{"when": "evening", "focus": "general", "location_name": "New York", "latitude": "40.7128", "longitude": "-74.0060", "timezone": "America/New_York"},
			output: "This evening in New York around 6 PM: overcast skies and 66°F with a 25% chance of precipitation.",
		},
		{
			name:   "tomorrow_temperature_at_6am",
			args:   map[string]any{"when": "tomorrow", "focus": "temperature", "hour_local": 6, "location_name": "New York", "latitude": "40.7128", "longitude": "-74.0060", "timezone": "America/New_York"},
			output: "Tomorrow in New York at 6 AM: 57.3°F.",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(tt.args)
			if err != nil {
				t.Fatalf("marshal args: %v", err)
			}
			result, err := exec.Execute(context.Background(), Request{
				Action: "weather",
				Args:   raw,
			})
			if err != nil {
				t.Fatalf("Execute returned error: %v", err)
			}
			if got := result.Output; got != tt.output {
				t.Fatalf("unexpected output: got %q want %q", got, tt.output)
			}
		})
	}
}

func TestGeocodeWeatherLocationRetriesNormalizedVariants(t *testing.T) {
	t.Parallel()

	queries := make([]string, 0, 4)
	location, err := GeocodeWeatherLocation(context.Background(), WeatherConfig{
		GeocodingURL: "https://geocoding-api.open-meteo.com/v1/search",
		Fetch: func(ctx context.Context, requestURL string) ([]byte, error) {
			u, err := url.Parse(requestURL)
			if err != nil {
				return nil, err
			}
			query := u.Query().Get("name")
			queries = append(queries, query)
			if query == "new york city" {
				return []byte(`{"results":[{"name":"New York","latitude":40.7128,"longitude":-74.0060,"timezone":"America/New_York","country":"United States"}]}`), nil
			}
			return []byte(`{"results":[]}`), nil
		},
	}, "new york city, ny")
	if err != nil {
		t.Fatalf("GeocodeWeatherLocation returned error: %v", err)
	}
	if got, want := location.Name, "New York, United States"; got != want {
		t.Fatalf("unexpected location name: got %q want %q", got, want)
	}
	if got, want := strings.Join(queries, " -> "), "new york city, ny -> new york city"; got != want {
		t.Fatalf("unexpected geocode retry order: got %q want %q", got, want)
	}
}

func TestResolveOlderSisterCallFillsQueryAndWebSearch(t *testing.T) {
	t.Parallel()

	call, err := ResolveOlderSisterCall(PlannedCall{
		Action: "older_sister",
		Args:   []byte(`{}`),
	}, "search the internet for current vllm embedding support")
	if err != nil {
		t.Fatalf("ResolveOlderSisterCall returned error: %v", err)
	}
	got := string(call.Args)
	for _, fragment := range []string{
		`"query":"search the internet for current vllm embedding support"`,
		`"web_search":true`,
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("resolved older_sister args missing %s in %s", fragment, got)
		}
	}
}

func TestOlderSisterCallsResponsesAPIWithWebSearch(t *testing.T) {
	var sawAuth bool
	var sawWebSearch bool
	originalDo := olderSisterHTTPDo
	t.Cleanup(func() {
		olderSisterHTTPDo = originalDo
	})
	olderSisterHTTPDo = func(r *http.Request) (*http.Response, error) {
		if got, want := r.Header.Get("Authorization"), "Bearer test-key"; got == want {
			sawAuth = true
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		if tools, ok := payload["tools"].([]any); ok && len(tools) > 0 {
			if first, ok := tools[0].(map[string]any); ok && first["type"] == "web_search" {
				sawWebSearch = true
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(bytes.NewBufferString(`{"output":[{"type":"message","content":[{"type":"output_text","text":"Older sister answer."}]}]}`)),
			Header:     make(http.Header),
		}, nil
	}

	exec := NewLocalExecutorWithConfigs(WeatherConfig{}, OlderSisterConfig{
		APIKey:    "test-key",
		HTTPURL:   "https://api.openai.com/v1/responses",
		Model:     "gpt-5-mini",
		TimeoutMs: 500,
		WebSearch: true,
	})
	raw, err := json.Marshal(map[string]any{
		"query":      "what changed in the latest docs?",
		"web_search": true,
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := exec.Execute(context.Background(), Request{Action: "older_sister", Args: raw})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got, want := result.Output, "Older sister answer."; got != want {
		t.Fatalf("unexpected output: got %q want %q", got, want)
	}
	if !sawAuth {
		t.Fatal("expected Authorization header")
	}
	if !sawWebSearch {
		t.Fatal("expected web_search tool in request")
	}
}

func TestResolveCalculatorCallFillsConvertArgsFromExplicitText(t *testing.T) {
	t.Parallel()

	call, err := ResolveCalculatorCall(PlannedCall{
		Action: "calculator",
		Args:   []byte(`{"operation":"convert"}`),
	}, "what is 103lbs in kg?")
	if err != nil {
		t.Fatalf("ResolveCalculatorCall returned error: %v", err)
	}

	got := string(call.Args)
	for _, fragment := range []string{
		`"operation":"convert"`,
		`"input":[{"unit":"lb","value":103}]`,
		`"to_unit":"kg"`,
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("resolved convert args missing %s in %s", fragment, got)
		}
	}
}

func TestCalculatorAmbiguousHeightLikeInchesNeedsClarification(t *testing.T) {
	t.Parallel()

	exec := NewLocalExecutor()
	raw, err := json.Marshal(map[string]any{
		"operation": "convert",
		"input": []map[string]any{
			{"unit": "in", "value": 5},
			{"unit": "in", "value": 4},
		},
		"to_unit": "cm",
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	result, err := exec.Execute(context.Background(), Request{Action: "calculator", Args: raw})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got, want := result.Status, "needs_input"; got != want {
		t.Fatalf("unexpected status: got %q want %q", got, want)
	}
	if !strings.Contains(result.Question, "feet plus inches") {
		t.Fatalf("unexpected clarification question: %q", result.Question)
	}
}

func TestCalculatorUnrelatedMultipleMeasurementsNeedClarification(t *testing.T) {
	t.Parallel()

	exec := NewLocalExecutor()
	raw, err := json.Marshal(map[string]any{
		"operation": "convert",
		"input": []map[string]any{
			{"unit": "in", "value": 5},
			{"unit": "in", "value": 4},
			{"unit": "m", "value": 10},
		},
		"to_unit": "cm",
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	result, err := exec.Execute(context.Background(), Request{Action: "calculator", Args: raw})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got, want := result.Status, "needs_input"; got != want {
		t.Fatalf("unexpected status: got %q want %q", got, want)
	}
	if !strings.Contains(result.Question, "exact value") {
		t.Fatalf("unexpected clarification question: %q", result.Question)
	}
}

func TestValidateCalculatorCallCatchesAmbiguousHeightTranscript(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(map[string]any{
		"operation": "convert",
		"input": []map[string]any{
			{"unit": "ft", "value": 5},
			{"unit": "ft", "value": 5},
		},
		"to_unit": "cm",
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	result, handled, err := ValidateCalculatorCall(PlannedCall{
		Action: "calculator",
		Args:   raw,
	}, "convert 5 5 ft")
	if err != nil {
		t.Fatalf("ValidateCalculatorCall returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected ambiguous transcript to be handled")
	}
	if got, want := result.Status, "needs_input"; got != want {
		t.Fatalf("unexpected status: got %q want %q", got, want)
	}
	if !strings.Contains(result.Question, "5 feet 5 inches") {
		t.Fatalf("unexpected question: %q", result.Question)
	}
}

func TestValidateCalculatorCallCatchesAdjacentWeightUnits(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(map[string]any{
		"operation": "convert",
		"input": []map[string]any{
			{"unit": "kg", "value": 152},
		},
		"to_unit": "lb",
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	result, handled, err := ValidateCalculatorCall(PlannedCall{
		Action: "calculator",
		Args:   raw,
	}, "152 kg lbs")
	if err != nil {
		t.Fatalf("ValidateCalculatorCall returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected adjacent weight units to need confirmation")
	}
	if got, want := result.Status, "needs_input"; got != want {
		t.Fatalf("unexpected status: got %q want %q", got, want)
	}
	if !strings.Contains(result.Question, "152 kg to lb") {
		t.Fatalf("unexpected question: %q", result.Question)
	}
}

func TestValidateCalculatorCallCatchesAdjacentWeightUnitsEvenWhenModelPickedBMI(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(map[string]any{
		"operation": "bmi",
		"weight": []map[string]any{
			{"unit": "kg", "value": 152},
		},
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	result, handled, err := ValidateCalculatorCall(PlannedCall{
		Action: "calculator",
		Args:   raw,
	}, "152 kg lbs")
	if err != nil {
		t.Fatalf("ValidateCalculatorCall returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected adjacent weight units to override BMI choice with clarification")
	}
	if got, want := result.Status, "needs_input"; got != want {
		t.Fatalf("unexpected status: got %q want %q", got, want)
	}
	if !strings.Contains(result.Question, "152 kg to lb") {
		t.Fatalf("unexpected question: %q", result.Question)
	}
}

func TestValidateCalculatorCallCatchesUnrelatedMultipleMeasurements(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(map[string]any{
		"operation": "convert",
		"input": []map[string]any{
			{"unit": "in", "value": 5},
			{"unit": "in", "value": 4},
			{"unit": "m", "value": 10},
		},
		"to_unit": "cm",
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	result, handled, err := ValidateCalculatorCall(PlannedCall{
		Action: "calculator",
		Args:   raw,
	}, "What is 5, 4 inches and 10 meters?")
	if err != nil {
		t.Fatalf("ValidateCalculatorCall returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected unrelated multiple measurements to need clarification")
	}
	if got, want := result.Status, "needs_input"; got != want {
		t.Fatalf("unexpected status: got %q want %q", got, want)
	}
	if !strings.Contains(result.Question, "multiple measurements") {
		t.Fatalf("unexpected question: %q", result.Question)
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
	}, "")
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
