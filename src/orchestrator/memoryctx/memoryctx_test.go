package memoryctx

import (
	"fmt"
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
	if got, want := strings.Join(subjects[0].Aliases, ","), "serene"; got != want {
		t.Fatalf("unexpected aliases: got %q want %q", got, want)
	}
}

func TestStoreRemembersSubjectRelationshipsAcrossSessions(t *testing.T) {
	t.Parallel()

	store := NewStore()
	err := store.RememberSubjectRelationships([]subjectctx.Relationship{
		{OwnerID: "person:serene", Relation: "girlfriend", SubjectID: "scoped:person_serene:girlfriend:sabrina"},
		{OwnerID: "person:sabrina", Relation: "girlfriend", SubjectID: "scoped:person_sabrina:girlfriend:serene"},
		{OwnerID: "self", Relation: "girlfriend", SubjectID: "person:sabrina"},
	})
	if err != nil {
		t.Fatalf("RememberSubjectRelationships returned error: %v", err)
	}

	relationships, err := store.LoadSubjectRelationships()
	if err != nil {
		t.Fatalf("LoadSubjectRelationships returned error: %v", err)
	}
	if len(relationships) != 2 {
		t.Fatalf("unexpected relationships: %#v", relationships)
	}
	if got, want := relationships[0], (subjectctx.Relationship{OwnerID: "person:sabrina", Relation: "girlfriend", SubjectID: "scoped:person_sabrina:girlfriend:serene"}); got != want {
		t.Fatalf("unexpected first relationship: got %#v want %#v", got, want)
	}
	if got, want := relationships[1], (subjectctx.Relationship{OwnerID: "person:serene", Relation: "girlfriend", SubjectID: "scoped:person_serene:girlfriend:sabrina"}); got != want {
		t.Fatalf("unexpected second relationship: got %#v want %#v", got, want)
	}
}

func TestStoreMirrorsFactsAndRelationshipsIntoDirectMemoryGraph(t *testing.T) {
	t.Parallel()

	store := NewStore()
	if err := store.RememberUserMessage("session-1", "person:serene", "My weight is 130lbs and my height is 5'8\""); err != nil {
		t.Fatalf("RememberUserMessage returned error: %v", err)
	}
	if err := store.RememberSubjectRelationships([]subjectctx.Relationship{
		{OwnerID: "person:serene", Relation: "girlfriend", SubjectID: "scoped:person_serene:girlfriend:sabrina"},
	}); err != nil {
		t.Fatalf("RememberSubjectRelationships returned error: %v", err)
	}
	if err := store.RememberUserMessage("session-1", "scoped:person_serene:girlfriend:sabrina", "Sabrina weighs 46kg and is 162cm tall"); err != nil {
		t.Fatalf("RememberUserMessage girlfriend returned error: %v", err)
	}

	sereneEdges, err := store.MemoryEdges("person:serene")
	if err != nil {
		t.Fatalf("MemoryEdges returned error: %v", err)
	}
	wantEdges := map[string]MemoryEdge{
		"girlfriend": {
			OwnerID:  "person:serene",
			Label:    "girlfriend",
			TargetID: "scoped:person_serene:girlfriend:sabrina",
		},
		"height": {
			OwnerID:  "person:serene",
			Label:    "height",
			TargetID: memoryValueNodeID("person:serene", "height"),
		},
		"weight": {
			OwnerID:  "person:serene",
			Label:    "weight",
			TargetID: memoryValueNodeID("person:serene", "weight"),
		},
	}
	if len(sereneEdges) != len(wantEdges) {
		t.Fatalf("unexpected Serene graph edges: %#v", sereneEdges)
	}
	for _, edge := range sereneEdges {
		want, ok := wantEdges[edge.Label]
		if !ok {
			t.Fatalf("unexpected graph edge: %#v", edge)
		}
		if edge != want {
			t.Fatalf("unexpected %s edge: got %#v want %#v", edge.Label, edge, want)
		}
	}

	sereneWeightValues, err := store.MemoryValues(memoryValueNodeID("person:serene", "weight"))
	if err != nil {
		t.Fatalf("MemoryValues Serene weight returned error: %v", err)
	}
	if len(sereneWeightValues) != 1 || !strings.Contains(string(sereneWeightValues[0].CanonicalValue), `"value":58.9670081`) {
		t.Fatalf("unexpected Serene weight values: %#v", sereneWeightValues)
	}

	girlfriendEdges, err := store.MemoryEdges("scoped:person_serene:girlfriend:sabrina")
	if err != nil {
		t.Fatalf("MemoryEdges girlfriend returned error: %v", err)
	}
	wantGirlfriendEdges := map[string]string{
		"height": memoryValueNodeID("scoped:person_serene:girlfriend:sabrina", "height"),
		"weight": memoryValueNodeID("scoped:person_serene:girlfriend:sabrina", "weight"),
	}
	if len(girlfriendEdges) != len(wantGirlfriendEdges) {
		t.Fatalf("unexpected girlfriend graph edges: %#v", girlfriendEdges)
	}
	for _, edge := range girlfriendEdges {
		if got, want := edge.TargetID, wantGirlfriendEdges[edge.Label]; got != want {
			t.Fatalf("unexpected girlfriend edge %#v want target %q", edge, want)
		}
	}
}

func TestStoreRetainsManyGenericFactsForOneIdentity(t *testing.T) {
	t.Parallel()

	store := NewStore()
	for idx := 1; idx <= 100; idx++ {
		text := fmt.Sprintf("my detail %03d is value-%03d", idx, idx)
		if err := store.RememberUserMessage("long-session", "person:serene", text); err != nil {
			t.Fatalf("RememberUserMessage %d returned error: %v", idx, err)
		}
	}

	snapshot := store.Snapshot("long-session", "person:serene")
	if len(snapshot) != 100 {
		t.Fatalf("unexpected snapshot size: got %d want 100", len(snapshot))
	}
	for _, idx := range []int{1, 42, 100} {
		attr := fmt.Sprintf("detail_%03d", idx)
		want := fmt.Sprintf(`"value-%03d"`, idx)
		if got := string(snapshot[attr]); got != want {
			t.Fatalf("unexpected %s value: got %s want %s", attr, got, want)
		}
	}

	edges, err := store.MemoryEdges("person:serene")
	if err != nil {
		t.Fatalf("MemoryEdges returned error: %v", err)
	}
	if len(edges) != 100 {
		t.Fatalf("unexpected edge count: got %d want 100", len(edges))
	}
}

func TestStoreRecordsConversationMessages(t *testing.T) {
	t.Parallel()

	store := NewStore()
	if err := store.RecordConversationMessage("session-1", "person:serene", "user", "first"); err != nil {
		t.Fatalf("RecordConversationMessage first returned error: %v", err)
	}
	if err := store.RecordConversationMessage("session-1", "", "assistant", "second"); err != nil {
		t.Fatalf("RecordConversationMessage second returned error: %v", err)
	}
	if err := store.RecordConversationMessage("session-1", "person:serene", "user", "third"); err != nil {
		t.Fatalf("RecordConversationMessage third returned error: %v", err)
	}

	messages, err := store.ConversationMessages("session-1", 2)
	if err != nil {
		t.Fatalf("ConversationMessages returned error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("unexpected message count: %#v", messages)
	}
	if got, want := messages[0].Content, "second"; got != want {
		t.Fatalf("unexpected first returned message: got %q want %q", got, want)
	}
	if got, want := messages[1].Content, "third"; got != want {
		t.Fatalf("unexpected second returned message: got %q want %q", got, want)
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

func TestStoreRemembersTextFactsFromUserMessages(t *testing.T) {
	t.Parallel()

	store := NewStore()
	if err := store.RememberUserMessage("session-1", "self", "my birthday is June 4"); err != nil {
		t.Fatalf("RememberUserMessage returned error: %v", err)
	}
	if err := store.RememberUserMessage("session-1", "self", "I started my new job on January 8, 2026"); err != nil {
		t.Fatalf("RememberUserMessage returned error: %v", err)
	}

	birthday, ok, err := store.LookupAttribute("self", "birthday")
	if err != nil {
		t.Fatalf("LookupAttribute birthday returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected birthday observation")
	}
	if got, want := string(birthday.RawValue), `"June 4"`; got != want {
		t.Fatalf("unexpected birthday: got %s want %s", got, want)
	}

	startDate, ok, err := store.LookupAttribute("self", "start_date")
	if err != nil {
		t.Fatalf("LookupAttribute start_date returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected start_date observation")
	}
	if got, want := string(startDate.RawValue), `"January 8, 2026"`; got != want {
		t.Fatalf("unexpected start date: got %s want %s", got, want)
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
