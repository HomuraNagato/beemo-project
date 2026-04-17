package memoryctx

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"eve-beemo/src/orchestrator/embedding"
	"eve-beemo/src/orchestrator/subjectctx"
	orchtools "eve-beemo/src/orchestrator/tools"
)

const (
	SourceTypeExplicitUser     = "explicit_user"
	SourceTypeResolvedToolArgs = "resolved_tool_args"
)

type Observation struct {
	SessionID       string
	Attribute       string
	Domain          string
	Route           string
	RawValue        json.RawMessage
	CanonicalValue  json.RawMessage
	ObservationText string
	EmbeddingModel  string
	Embedding       []float32
	SourceTurn      string
	SourceType      string
	CreatedAt       time.Time
}

type RecordContext struct {
	Domain     string
	Route      string
	SourceTurn string
	SourceType string
}

type SnapshotDetails struct {
	Values    map[string]json.RawMessage
	Conflicts map[string][]Observation
	Err       error
}

type RecallMatch struct {
	Observation Observation
	Score       float32
}

func (s *Store) LookupAttribute(subjectID, attr string) (Observation, bool, error) {
	if s.db != nil {
		return s.lookupAttributeDB(subjectID, attr)
	}
	if strings.TrimSpace(subjectID) == "" || strings.TrimSpace(attr) == "" {
		return Observation{}, false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	subject := s.subjects[subjectID]
	if subject == nil {
		return Observation{}, false, nil
	}
	observations := subject.observations[strings.TrimSpace(attr)]
	return preferredObservationFromAscendingHistory(observations)
}

type Store struct {
	mu       sync.Mutex
	subjects map[string]*subjectState
	aliases  map[string]map[string]struct{}
	db       *sql.DB
	httpURL  string
	model    string
	timeout  time.Duration
	embedFn  func(httpURL, model string, inputs []string, timeout time.Duration) ([][]float32, error)
}

type subjectState struct {
	observations map[string][]Observation
}

func NewStore() *Store {
	return &Store{
		subjects: map[string]*subjectState{},
		aliases:  map[string]map[string]struct{}{},
		embedFn:  embedding.Call,
	}
}

func NewPostgresStore(db *sql.DB) *Store {
	return &Store{
		db:      db,
		embedFn: embedding.Call,
	}
}

func (s *Store) WithEmbeddings(httpURL, model string, timeout time.Duration) *Store {
	if s == nil {
		return nil
	}
	s.httpURL = strings.TrimSpace(httpURL)
	s.model = strings.TrimSpace(model)
	s.timeout = timeout
	if s.embedFn == nil {
		s.embedFn = embedding.Call
	}
	return s
}

func (s *Store) WithEmbedder(fn func(httpURL, model string, inputs []string, timeout time.Duration) ([][]float32, error)) *Store {
	if s == nil {
		return nil
	}
	s.embedFn = fn
	return s
}

func (s *Store) RememberUserMessage(sessionID, subjectID, text string, attrs ...string) error {
	return s.RememberUserMessageWithContext(sessionID, subjectID, text, RecordContext{
		SourceTurn: text,
		SourceType: SourceTypeExplicitUser,
	}, attrs...)
}

func (s *Store) RememberUserMessageWithContext(sessionID, subjectID, text string, ctx RecordContext, attrs ...string) error {
	patch, ok, err := orchtools.ExtractCalculatorObservationPatch(text)
	if err != nil || !ok {
		return err
	}
	if strings.TrimSpace(ctx.SourceTurn) == "" {
		ctx.SourceTurn = text
	}
	if strings.TrimSpace(ctx.SourceType) == "" {
		ctx.SourceType = SourceTypeExplicitUser
	}
	if s.db != nil {
		return s.applyPatchDB(sessionID, subjectID, patch, ctx, attrs...)
	}
	return s.applyPatch(sessionID, subjectID, patch, ctx, attrs...)
}

func (s *Store) RememberToolCall(sessionID, subjectID string, call orchtools.PlannedCall, source string, attrs ...string) error {
	if strings.TrimSpace(source) == "" {
		source = SourceTypeResolvedToolArgs
	}
	return s.RememberToolCallWithContext(sessionID, subjectID, call, RecordContext{
		SourceType: source,
	}, attrs...)
}

func (s *Store) RememberToolCallWithContext(sessionID, subjectID string, call orchtools.PlannedCall, ctx RecordContext, attrs ...string) error {
	if strings.TrimSpace(call.Action) != "calculator" {
		return nil
	}
	patch, ok, err := patchFromCalculatorCall(call.Args)
	if err != nil || !ok {
		return err
	}
	if strings.TrimSpace(ctx.SourceType) == "" {
		ctx.SourceType = SourceTypeResolvedToolArgs
	}
	if s.db != nil {
		return s.applyPatchDB(sessionID, subjectID, patch, ctx, attrs...)
	}
	return s.applyPatch(sessionID, subjectID, patch, ctx, attrs...)
}

func (s *Store) HydrateCall(sessionID, subjectID string, call orchtools.PlannedCall) (orchtools.PlannedCall, error) {
	if strings.TrimSpace(call.Action) != "calculator" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(subjectID) == "" {
		return call, nil
	}

	args := map[string]json.RawMessage{}
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return orchtools.PlannedCall{}, fmt.Errorf("invalid calculator args for hydration: %w", err)
		}
	}

	attrs := relevantAttrsForOperation(stringFieldRaw(args["operation"]))
	if len(attrs) == 0 {
		return call, nil
	}

	details := s.SnapshotDetails(sessionID, subjectID, attrs...)
	if details.Err != nil {
		return orchtools.PlannedCall{}, details.Err
	}
	snapshot := details.Values
	changed := false
	for _, attr := range attrs {
		if hasValue(attr, args[attr]) {
			continue
		}
		if raw, ok := snapshot[attr]; ok && hasValue(attr, raw) {
			args[attr] = cloneRaw(raw)
			changed = true
		}
	}
	if !changed {
		return call, nil
	}

	updated, err := json.Marshal(args)
	if err != nil {
		return orchtools.PlannedCall{}, err
	}
	call.Args = updated
	return call, nil
}

func (s *Store) Snapshot(sessionID, subjectID string, attrs ...string) map[string]json.RawMessage {
	return s.SnapshotDetails(sessionID, subjectID, attrs...).Values
}

func (s *Store) Recall(subjectID, query string, limit int, timeout time.Duration) ([]RecallMatch, error) {
	if s.db != nil {
		return s.recallDB(subjectID, query, limit, timeout)
	}
	if strings.TrimSpace(subjectID) == "" || strings.TrimSpace(query) == "" || limit <= 0 {
		return nil, nil
	}
	if !s.embeddingEnabled() {
		return nil, nil
	}

	queryVector, err := s.embedQuery(strings.TrimSpace(query), timeout)
	if err != nil || len(queryVector) == 0 {
		return nil, err
	}

	s.mu.Lock()
	subject := s.subjects[subjectID]
	if subject == nil {
		s.mu.Unlock()
		return nil, nil
	}
	matches := make([]RecallMatch, 0, len(subject.observations))
	for _, observations := range subject.observations {
		for _, observation := range observations {
			if len(observation.Embedding) == 0 {
				continue
			}
			score := cosineSimilarity(queryVector, observation.Embedding)
			if score < 0 {
				continue
			}
			matches = append(matches, RecallMatch{
				Observation: cloneObservation(observation),
				Score:       score,
			})
		}
	}
	s.mu.Unlock()

	if len(matches) == 0 {
		return nil, nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].Observation.CreatedAt.After(matches[j].Observation.CreatedAt)
		}
		return matches[i].Score > matches[j].Score
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func (s *Store) SnapshotDetails(sessionID, subjectID string, attrs ...string) SnapshotDetails {
	if s.db != nil {
		return s.snapshotDetailsDB(sessionID, subjectID, attrs...)
	}
	if strings.TrimSpace(subjectID) == "" {
		return SnapshotDetails{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	subject := s.subjects[subjectID]
	if subject == nil {
		return SnapshotDetails{}
	}

	allowed := attrSet(attrs)
	values := make(map[string]json.RawMessage, len(subject.observations))
	conflicts := map[string][]Observation{}
	for attr, observations := range subject.observations {
		if len(allowed) > 0 {
			if _, ok := allowed[attr]; !ok {
				continue
			}
		}
		if len(observations) == 0 {
			continue
		}
		if value := latestObservationValue(observations); len(value) > 0 {
			values[attr] = value
		}
		if distinct := conflictingExplicitObservations(observations); len(distinct) > 1 {
			conflicts[attr] = distinct
		}
	}
	if len(values) == 0 && len(conflicts) == 0 {
		return SnapshotDetails{}
	}
	return SnapshotDetails{
		Values:    values,
		Conflicts: conflicts,
	}
}

func (s *Store) applyPatch(sessionID, subjectID string, patch json.RawMessage, ctx RecordContext, attrs ...string) error {
	if strings.TrimSpace(subjectID) == "" || len(patch) == 0 {
		return nil
	}

	observations, err := s.observationsFromPatch(sessionID, patch, ctx, attrs...)
	if err != nil {
		return err
	}
	if len(observations) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	subject := s.subjects[subjectID]
	if subject == nil {
		subject = &subjectState{observations: map[string][]Observation{}}
		s.subjects[subjectID] = subject
	}

	for _, observation := range observations {
		attr := observation.Attribute
		history := subject.observations[attr]
		if len(history) > 0 && observationsEqual(history[len(history)-1], observation) {
			continue
		}
		subject.observations[attr] = append(history, observation)
	}
	return nil
}

func (s *Store) observationsFromPatch(sessionID string, patch json.RawMessage, ctx RecordContext, attrs ...string) ([]Observation, error) {
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(patch, &values); err != nil {
		return nil, fmt.Errorf("invalid observation patch: %w", err)
	}
	if len(values) == 0 {
		return nil, nil
	}
	allowed := attrSet(attrs)
	now := time.Now().UTC()

	observations := make([]Observation, 0, len(values))
	for attr, raw := range values {
		if len(allowed) > 0 {
			if _, ok := allowed[attr]; !ok {
				continue
			}
		}
		if !hasValue(attr, raw) {
			continue
		}
		raw = cloneRaw(raw)
		canonical, err := orchtools.CanonicalizeObservationValue(attr, raw)
		if err != nil {
			return nil, err
		}
		observation := Observation{
			SessionID:       strings.TrimSpace(sessionID),
			Attribute:       attr,
			Domain:          strings.TrimSpace(ctx.Domain),
			Route:           strings.TrimSpace(ctx.Route),
			RawValue:        raw,
			CanonicalValue:  cloneRaw(canonical),
			ObservationText: observationText(attr, raw, canonical, ctx),
			SourceTurn:      strings.TrimSpace(ctx.SourceTurn),
			SourceType:      strings.TrimSpace(ctx.SourceType),
			CreatedAt:       now,
		}
		observations = append(observations, observation)
	}
	if len(observations) == 0 {
		return nil, nil
	}
	s.attachObservationEmbeddings(observations, s.timeout)
	return observations, nil
}

func (s *Store) RememberSubjectAliases(sessionID string, subjects []subjectctx.Subject) error {
	if s.db != nil {
		return s.rememberSubjectAliasesDB(sessionID, subjects)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.aliases == nil {
		s.aliases = map[string]map[string]struct{}{}
	}
	for _, subject := range subjects {
		subjectID := strings.TrimSpace(subject.ID)
		if subjectID == "" {
			continue
		}
		for _, alias := range subject.Aliases {
			alias = normalizeAliasForStorage(alias)
			if !shouldPersistAlias(subjectID, alias) {
				continue
			}
			if s.aliases[subjectID] == nil {
				s.aliases[subjectID] = map[string]struct{}{}
			}
			s.aliases[subjectID][alias] = struct{}{}
		}
	}
	return nil
}

func (s *Store) LoadSubjectAliases() ([]subjectctx.Subject, error) {
	if s.db != nil {
		return s.loadSubjectAliasesDB()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.aliases) == 0 {
		return nil, nil
	}
	subjectIDs := make([]string, 0, len(s.aliases))
	for subjectID := range s.aliases {
		subjectIDs = append(subjectIDs, subjectID)
	}
	sort.Strings(subjectIDs)

	subjects := make([]subjectctx.Subject, 0, len(subjectIDs))
	for _, subjectID := range subjectIDs {
		aliasSet := s.aliases[subjectID]
		if len(aliasSet) == 0 {
			continue
		}
		aliases := make([]string, 0, len(aliasSet))
		for alias := range aliasSet {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		subjects = append(subjects, subjectctx.Subject{
			ID:      subjectID,
			Aliases: aliases,
		})
	}
	return subjects, nil
}

func attrSet(attrs []string) map[string]struct{} {
	normalized := make(map[string]struct{}, len(attrs))
	for _, attr := range attrs {
		attr = strings.TrimSpace(attr)
		if attr == "" {
			continue
		}
		normalized[attr] = struct{}{}
	}
	return normalized
}

func patchFromCalculatorCall(raw json.RawMessage) (json.RawMessage, bool, error) {
	args := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, false, fmt.Errorf("invalid calculator args for observation patch: %w", err)
		}
	}

	attrs := relevantAttrsForOperation(stringFieldRaw(args["operation"]))
	if len(attrs) == 0 {
		return nil, false, nil
	}

	patch := make(map[string]json.RawMessage, len(attrs))
	for _, attr := range attrs {
		if value, ok := args[attr]; ok && hasValue(attr, value) {
			patch[attr] = cloneRaw(value)
		}
	}
	if len(patch) == 0 {
		return nil, false, nil
	}

	encoded, err := json.Marshal(patch)
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}

func latestObservationValue(observations []Observation) json.RawMessage {
	for idx := len(observations) - 1; idx >= 0; idx-- {
		if value := observationValue(observations[idx]); len(value) > 0 {
			return value
		}
	}
	return nil
}

func conflictingExplicitObservations(observations []Observation) []Observation {
	distinct := map[string]struct{}{}
	ordered := make([]Observation, 0, len(observations))
	for idx := len(observations) - 1; idx >= 0; idx-- {
		observation := observations[idx]
		if !isExplicitObservation(observation) {
			continue
		}
		key := comparableObservationValue(observation)
		if key == "" {
			continue
		}
		if _, exists := distinct[key]; exists {
			continue
		}
		distinct[key] = struct{}{}
		ordered = append(ordered, cloneObservation(observation))
	}
	if len(ordered) <= 1 {
		return nil
	}
	return ordered
}

func preferredObservationFromAscendingHistory(observations []Observation) (Observation, bool, error) {
	for idx := len(observations) - 1; idx >= 0; idx-- {
		observation := observations[idx]
		if !isExplicitObservation(observation) || len(observation.RawValue) == 0 {
			continue
		}
		return cloneObservation(observation), true, nil
	}
	for idx := len(observations) - 1; idx >= 0; idx-- {
		observation := cloneObservation(observations[idx])
		if len(observation.CanonicalValue) > 0 || len(observation.RawValue) > 0 {
			return observation, true, nil
		}
	}
	return Observation{}, false, nil
}

func isExplicitObservation(observation Observation) bool {
	return strings.TrimSpace(observation.SourceType) == SourceTypeExplicitUser
}

func comparableObservationValue(observation Observation) string {
	value := observationValue(observation)
	switch observation.Attribute {
	case "weight", "height":
		var items []struct {
			Unit  string  `json:"unit"`
			Value float64 `json:"value"`
		}
		if err := json.Unmarshal(value, &items); err == nil && len(items) > 0 {
			parts := make([]string, 0, len(items))
			for _, item := range items {
				parts = append(parts, fmt.Sprintf("%s:%.6f", strings.TrimSpace(item.Unit), item.Value))
			}
			return strings.Join(parts, "|")
		}
	case "age_years":
		var years float64
		if err := json.Unmarshal(value, &years); err == nil {
			return fmt.Sprintf("%.6f", years)
		}
	}
	return string(value)
}

func observationValue(observation Observation) json.RawMessage {
	if len(observation.CanonicalValue) > 0 {
		return cloneRaw(observation.CanonicalValue)
	}
	return cloneRaw(observation.RawValue)
}

func observationsEqual(a, b Observation) bool {
	return a.Attribute == b.Attribute &&
		a.Domain == b.Domain &&
		a.Route == b.Route &&
		string(a.RawValue) == string(b.RawValue) &&
		string(a.CanonicalValue) == string(b.CanonicalValue) &&
		a.SourceTurn == b.SourceTurn &&
		a.SourceType == b.SourceType
}

func cloneObservation(observation Observation) Observation {
	return Observation{
		SessionID:       observation.SessionID,
		Attribute:       observation.Attribute,
		Domain:          observation.Domain,
		Route:           observation.Route,
		RawValue:        cloneRaw(observation.RawValue),
		CanonicalValue:  cloneRaw(observation.CanonicalValue),
		ObservationText: observation.ObservationText,
		EmbeddingModel:  observation.EmbeddingModel,
		Embedding:       cloneEmbedding(observation.Embedding),
		SourceTurn:      observation.SourceTurn,
		SourceType:      observation.SourceType,
		CreatedAt:       observation.CreatedAt,
	}
}

func normalizeAliasForStorage(alias string) string {
	return strings.TrimSpace(alias)
}

func shouldPersistAlias(subjectID, alias string) bool {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return false
	}
	if subjectID == "self" {
		return false
	}
	switch alias {
	case "he", "him", "his", "she", "her", "hers", "they", "them", "their", "theirs", "i", "me", "my", "mine":
		return false
	default:
		return true
	}
}

func (s *Store) embeddingEnabled() bool {
	return s != nil && s.embedFn != nil && strings.TrimSpace(s.httpURL) != ""
}

func (s *Store) embedQuery(query string, timeout time.Duration) ([]float32, error) {
	if !s.embeddingEnabled() {
		return nil, nil
	}
	effectiveTimeout := timeout
	if effectiveTimeout <= 0 {
		effectiveTimeout = s.timeout
	}
	vectors, err := s.embedFn(s.httpURL, s.model, []string{memoryRecallInstruction(query)}, effectiveTimeout)
	if err != nil || len(vectors) == 0 {
		return nil, err
	}
	return cloneEmbedding(vectors[0]), nil
}

func (s *Store) attachObservationEmbeddings(observations []Observation, timeout time.Duration) {
	if !s.embeddingEnabled() || len(observations) == 0 {
		return
	}
	inputs := make([]string, 0, len(observations))
	indexes := make([]int, 0, len(observations))
	for idx := range observations {
		text := strings.TrimSpace(observations[idx].ObservationText)
		if text == "" {
			continue
		}
		inputs = append(inputs, text)
		indexes = append(indexes, idx)
	}
	if len(inputs) == 0 {
		return
	}
	effectiveTimeout := timeout
	if effectiveTimeout <= 0 {
		effectiveTimeout = s.timeout
	}
	vectors, err := s.embedFn(s.httpURL, s.model, inputs, effectiveTimeout)
	if err != nil || len(vectors) != len(inputs) {
		return
	}
	for i, idx := range indexes {
		observations[idx].EmbeddingModel = s.model
		observations[idx].Embedding = cloneEmbedding(vectors[i])
	}
}

func observationText(attr string, raw, canonical json.RawMessage, ctx RecordContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Attribute: %s\n", strings.TrimSpace(attr))
	if len(raw) > 0 {
		fmt.Fprintf(&b, "Raw value: %s\n", strings.TrimSpace(string(raw)))
	}
	if len(canonical) > 0 && string(canonical) != string(raw) {
		fmt.Fprintf(&b, "Canonical value: %s\n", strings.TrimSpace(string(canonical)))
	}
	if strings.TrimSpace(ctx.SourceType) != "" {
		fmt.Fprintf(&b, "Source type: %s\n", strings.TrimSpace(ctx.SourceType))
	}
	if strings.TrimSpace(ctx.SourceTurn) != "" {
		fmt.Fprintf(&b, "Source turn: %s\n", strings.TrimSpace(ctx.SourceTurn))
	}
	return strings.TrimSpace(b.String())
}

func memoryRecallInstruction(query string) string {
	return "Instruct: Given a user request to recall something about a resolved subject, retrieve the most relevant remembered observation.\nQuery: " + strings.TrimSpace(query)
}

func cosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return -1
	}
	var dot float64
	var magA float64
	var magB float64
	for i := range a {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		magA += af * af
		magB += bf * bf
	}
	if magA == 0 || magB == 0 {
		return -1
	}
	return float32(dot / (math.Sqrt(magA) * math.Sqrt(magB)))
}

func cloneEmbedding(input []float32) []float32 {
	if len(input) == 0 {
		return nil
	}
	cloned := make([]float32, len(input))
	copy(cloned, input)
	return cloned
}

func relevantAttrsForOperation(operation string) []string {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "bmi":
		return []string{"weight", "height"}
	case "bmr":
		return []string{"weight", "height", "age_years", "gender"}
	case "tdee":
		return []string{"weight", "height", "age_years", "gender", "activity_level"}
	default:
		return nil
	}
}

func hasValue(attr string, raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	switch attr {
	case "weight", "height":
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return false
		}
		return len(items) > 0
	case "age_years":
		var value *float64
		if err := json.Unmarshal(raw, &value); err != nil || value == nil {
			return false
		}
		return true
	case "gender", "activity_level":
		return strings.TrimSpace(stringFieldRaw(raw)) != ""
	default:
		return false
	}
}

func stringFieldRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	cloned := make([]byte, len(raw))
	copy(cloned, raw)
	return json.RawMessage(cloned)
}
