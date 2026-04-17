package memoryctx

import (
	"strings"
	"testing"

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

	observations := store.sessions["session-1"].subjects["person:serene"].observations["weight"]
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
