package memoryctx

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	orchtools "eve-beemo/src/orchestrator/tools"
)

const (
	SourceTypeExplicitUser     = "explicit_user"
	SourceTypeResolvedToolArgs = "resolved_tool_args"
)

type Observation struct {
	Attribute      string
	Domain         string
	Route          string
	RawValue       json.RawMessage
	CanonicalValue json.RawMessage
	SourceTurn     string
	SourceType     string
	CreatedAt      time.Time
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

type Store struct {
	mu       sync.Mutex
	sessions map[string]*sessionState
	db       *sql.DB
}

type sessionState struct {
	subjects map[string]*subjectState
}

type subjectState struct {
	observations map[string][]Observation
}

func NewStore() *Store {
	return &Store{
		sessions: map[string]*sessionState{},
	}
}

func NewPostgresStore(db *sql.DB) *Store {
	return &Store{db: db}
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

func (s *Store) SnapshotDetails(sessionID, subjectID string, attrs ...string) SnapshotDetails {
	if s.db != nil {
		return s.snapshotDetailsDB(sessionID, subjectID, attrs...)
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(subjectID) == "" {
		return SnapshotDetails{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session := s.sessions[sessionID]
	if session == nil {
		return SnapshotDetails{}
	}
	subject := session.subjects[subjectID]
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
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(subjectID) == "" || len(patch) == 0 {
		return nil
	}

	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(patch, &values); err != nil {
		return fmt.Errorf("invalid observation patch: %w", err)
	}
	if len(values) == 0 {
		return nil
	}
	allowed := attrSet(attrs)

	s.mu.Lock()
	defer s.mu.Unlock()

	session := s.sessions[sessionID]
	if session == nil {
		session = &sessionState{subjects: map[string]*subjectState{}}
		s.sessions[sessionID] = session
	}
	subject := session.subjects[subjectID]
	if subject == nil {
		subject = &subjectState{observations: map[string][]Observation{}}
		session.subjects[subjectID] = subject
	}

	now := time.Now().UTC()
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
			return err
		}
		observation := Observation{
			Attribute:      attr,
			Domain:         strings.TrimSpace(ctx.Domain),
			Route:          strings.TrimSpace(ctx.Route),
			RawValue:       raw,
			CanonicalValue: cloneRaw(canonical),
			SourceTurn:     strings.TrimSpace(ctx.SourceTurn),
			SourceType:     strings.TrimSpace(ctx.SourceType),
			CreatedAt:      now,
		}
		history := subject.observations[attr]
		if len(history) > 0 && observationsEqual(history[len(history)-1], observation) {
			continue
		}
		subject.observations[attr] = append(history, observation)
	}
	return nil
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
		Attribute:      observation.Attribute,
		Domain:         observation.Domain,
		Route:          observation.Route,
		RawValue:       cloneRaw(observation.RawValue),
		CanonicalValue: cloneRaw(observation.CanonicalValue),
		SourceTurn:     observation.SourceTurn,
		SourceType:     observation.SourceType,
		CreatedAt:      observation.CreatedAt,
	}
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
