package memoryctx

import (
	"strings"
	"testing"
	"time"

	"eve-beemo/src/orchestrator/subjectctx"
	orchtools "eve-beemo/src/orchestrator/tools"
)

func TestStoreHydratesTDEEFromRememberedObservations(t *testing.T) {
	t.Parallel()

	store := NewStore()
	if err := store.RememberUserMessage("session-1", "person:mark", "my brother Mark is a 34 year old male weighing 70kg and 180cm tall"); err != nil {
		t.Fatalf("RememberUserMessage returned error: %v", err)
	}
	if err := store.RememberUserMessage("session-1", "person:mark", "he is moderately active"); err != nil {
		t.Fatalf("RememberUserMessage returned error: %v", err)
	}

	call, err := store.HydrateCall("session-1", "person:mark", orchtools.PlannedCall{
		Action: "calculator",
		Args:   []byte(`{"operation":"tdee"}`),
	})
	if err != nil {
		t.Fatalf("HydrateCall returned error: %v", err)
	}

	got := string(call.Args)
	for _, fragment := range []string{
		`"age_years":34`,
		`"gender":"male"`,
		`"activity_level":"moderate"`,
		`"weight":[{"unit":"kg","value":70}]`,
		`"height":[{"unit":"cm","value":180}]`,
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("hydrated args missing %s in %s", fragment, got)
		}
	}
}

func TestStoreRemembersGroundedCalculatorArgs(t *testing.T) {
	t.Parallel()

	store := NewStore()
	err := store.RememberToolCall("session-1", "self", orchtools.PlannedCall{
		Action: "calculator",
		Args:   []byte(`{"operation":"bmi","weight":[{"unit":"kg","value":45}],"height":[{"unit":"cm","value":162}]}`),
	}, "grounded_tool_args")
	if err != nil {
		t.Fatalf("RememberToolCall returned error: %v", err)
	}

	snapshot := store.Snapshot("session-1", "self")
	if got := string(snapshot["weight"]); got != `[{"unit":"kg","value":45}]` {
		t.Fatalf("unexpected stored weight: %s", got)
	}
	if got := string(snapshot["height"]); got != `[{"unit":"cm","value":162}]` {
		t.Fatalf("unexpected stored height: %s", got)
	}
}

func TestStoreFiltersWritesAndSnapshotByAttrs(t *testing.T) {
	t.Parallel()

	store := NewStore()
	if err := store.RememberUserMessage("session-1", "self", "I am 35 years old, female, 45kg, and 162cm", "weight", "height"); err != nil {
		t.Fatalf("RememberUserMessage returned error: %v", err)
	}

	snapshot := store.Snapshot("session-1", "self")
	if got := string(snapshot["weight"]); got != `[{"unit":"kg","value":45}]` {
		t.Fatalf("unexpected stored weight: %s", got)
	}
	if got := string(snapshot["height"]); got != `[{"unit":"cm","value":162}]` {
		t.Fatalf("unexpected stored height: %s", got)
	}
	if _, ok := snapshot["age_years"]; ok {
		t.Fatalf("did not expect age_years in snapshot: %#v", snapshot)
	}
	if _, ok := snapshot["gender"]; ok {
		t.Fatalf("did not expect gender in snapshot: %#v", snapshot)
	}

	filtered := store.Snapshot("session-1", "self", "height")
	if len(filtered) != 1 {
		t.Fatalf("unexpected filtered snapshot size: %#v", filtered)
	}
	if got := string(filtered["height"]); got != `[{"unit":"cm","value":162}]` {
		t.Fatalf("unexpected filtered height: %s", got)
	}
}

func TestStorePersistsObservationMetadataAndCanonicalValue(t *testing.T) {
	t.Parallel()

	store := NewStore()
	err := store.RememberUserMessageWithContext("session-1", "person:serene", "Serene weighs 134lbs", RecordContext{
		Domain:     "calculator",
		Route:      "calculator.bmi",
		SourceTurn: "Serene weighs 134lbs",
		SourceType: SourceTypeExplicitUser,
	}, "weight")
	if err != nil {
		t.Fatalf("RememberUserMessageWithContext returned error: %v", err)
	}

	observations := store.subjects["person:serene"].observations["weight"]
	if len(observations) != 1 {
		t.Fatalf("unexpected observation history: %#v", observations)
	}
	observation := observations[0]
	if got, want := string(observation.RawValue), `[{"unit":"lb","value":134}]`; got != want {
		t.Fatalf("unexpected raw value: got %s want %s", got, want)
	}
	if !strings.Contains(string(observation.CanonicalValue), `"unit":"kg"`) || !strings.Contains(string(observation.CanonicalValue), `"value":60.78137758`) {
		t.Fatalf("unexpected canonical value: %s", observation.CanonicalValue)
	}
	if got, want := observation.Domain, "calculator"; got != want {
		t.Fatalf("unexpected domain: got %q want %q", got, want)
	}
	if got, want := observation.Route, "calculator.bmi"; got != want {
		t.Fatalf("unexpected route: got %q want %q", got, want)
	}
	if got, want := observation.SourceTurn, "Serene weighs 134lbs"; got != want {
		t.Fatalf("unexpected source turn: got %q want %q", got, want)
	}
	if got, want := observation.SourceType, SourceTypeExplicitUser; got != want {
		t.Fatalf("unexpected source type: got %q want %q", got, want)
	}
	if observation.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be set")
	}
}

func TestStoreSnapshotDetailsReportsConflictsForDistinctExplicitValues(t *testing.T) {
	t.Parallel()

	store := NewStore()
	if err := store.RememberUserMessage("session-1", "self", "I weigh 45kg and I am 162cm tall"); err != nil {
		t.Fatalf("RememberUserMessage returned error: %v", err)
	}
	if err := store.RememberUserMessage("session-1", "self", "I weigh 50kg"); err != nil {
		t.Fatalf("RememberUserMessage returned error: %v", err)
	}

	details := store.SnapshotDetails("session-1", "self", "weight", "height")
	if got := string(details.Values["weight"]); got != `[{"unit":"kg","value":50}]` {
		t.Fatalf("unexpected latest weight in snapshot: %s", got)
	}
	if got := string(details.Values["height"]); got != `[{"unit":"cm","value":162}]` {
		t.Fatalf("unexpected latest height in snapshot: %s", got)
	}
	conflicts := details.Conflicts["weight"]
	if len(conflicts) != 2 {
		t.Fatalf("expected two conflicting weight observations, got %#v", conflicts)
	}
	if got := string(conflicts[0].CanonicalValue); got != `[{"unit":"kg","value":50}]` {
		t.Fatalf("unexpected latest conflict value: %s", got)
	}
	if got := string(conflicts[1].CanonicalValue); got != `[{"unit":"kg","value":45}]` {
		t.Fatalf("unexpected older conflict value: %s", got)
	}
	if _, ok := details.Conflicts["height"]; ok {
		t.Fatalf("did not expect height conflict: %#v", details.Conflicts)
	}
}

func TestStoreRemembersSubjectAliasesAcrossSessions(t *testing.T) {
	t.Parallel()

	store := NewStore()
	err := store.RememberSubjectAliases("session-1", []subjectctx.Subject{
		{ID: "person:serene", Aliases: []string{"serene", "sister", "my sister"}},
		{ID: "self", Aliases: []string{"i", "me", "my"}},
	})
	if err != nil {
		t.Fatalf("RememberSubjectAliases returned error: %v", err)
	}

	subjects, err := store.LoadSubjectAliases()
	if err != nil {
		t.Fatalf("LoadSubjectAliases returned error: %v", err)
	}
	if len(subjects) != 1 {
		t.Fatalf("unexpected persisted subjects: %#v", subjects)
	}
	if got, want := subjects[0].ID, "person:serene"; got != want {
		t.Fatalf("unexpected subject id: got %q want %q", got, want)
	}
	if got, want := strings.Join(subjects[0].Aliases, ","), "my sister,serene,sister"; got != want {
		t.Fatalf("unexpected aliases: got %q want %q", got, want)
	}
}

func TestStoreHydratesAcrossSessionsBySubjectID(t *testing.T) {
	t.Parallel()

	store := NewStore()
	if err := store.RememberUserMessage("session-1", "person:serene", "Serene is female, 27 years old, weighs 134lbs, and is 174cm tall"); err != nil {
		t.Fatalf("RememberUserMessage returned error: %v", err)
	}

	call, err := store.HydrateCall("session-2", "person:serene", orchtools.PlannedCall{
		Action: "calculator",
		Args:   []byte(`{"operation":"bmr"}`),
	})
	if err != nil {
		t.Fatalf("HydrateCall returned error: %v", err)
	}

	got := string(call.Args)
	for _, fragment := range []string{
		`"age_years":27`,
		`"gender":"female"`,
		`"height":[{"unit":"cm","value":174}]`,
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("hydrated args missing %s in %s", fragment, got)
		}
	}
	if !strings.Contains(got, `"weight":[{"unit":"kg","value":60.78137758`) {
		t.Fatalf("hydrated args missing canonical weight in %s", got)
	}
}

func TestStoreLookupAttributePrefersLatestExplicitRawObservation(t *testing.T) {
	t.Parallel()

	store := NewStore()
	if err := store.RememberUserMessage("session-1", "self", "I weigh 45kg and I am 64 inches tall"); err != nil {
		t.Fatalf("RememberUserMessage returned error: %v", err)
	}
	if err := store.RememberToolCallWithContext("session-1", "self", orchtools.PlannedCall{
		Action: "calculator",
		Args:   []byte(`{"operation":"bmi","weight":[{"unit":"kg","value":45}],"height":[{"unit":"cm","value":162.56}]}`),
	}, RecordContext{
		Domain:     "calculator",
		Route:      "calculator.bmi",
		SourceTurn: "what is my bmi?",
		SourceType: SourceTypeResolvedToolArgs,
	}); err != nil {
		t.Fatalf("RememberToolCallWithContext returned error: %v", err)
	}

	observation, ok, err := store.LookupAttribute("self", "height")
	if err != nil {
		t.Fatalf("LookupAttribute returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected stored height observation")
	}
	if got, want := string(observation.RawValue), `[{"unit":"in","value":64}]`; got != want {
		t.Fatalf("unexpected raw height: got %s want %s", got, want)
	}
}

func TestStoreRecallFindsRelevantObservationByEmbedding(t *testing.T) {
	t.Parallel()

	store := NewStore().WithEmbeddings("http://embed.test/v1/embeddings", "test-embed", 0).WithEmbedder(func(httpURL, model string, inputs []string, timeout time.Duration) ([][]float32, error) {
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

	if err := store.RememberUserMessage("session-1", "self", "I weigh 45kg and I am 64 inches tall"); err != nil {
		t.Fatalf("RememberUserMessage returned error: %v", err)
	}

	matches, err := store.Recall("self", "how tall am I?", 3, 0)
	if err != nil {
		t.Fatalf("Recall returned error: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected semantic recall matches")
	}
	if got, want := matches[0].Observation.Attribute, "height"; got != want {
		t.Fatalf("unexpected top recalled attribute: got %q want %q", got, want)
	}
	if got := matches[0].Observation.ObservationText; !strings.Contains(got, "Attribute: height") {
		t.Fatalf("unexpected recalled observation text: %q", got)
	}
}
