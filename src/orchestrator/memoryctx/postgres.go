package memoryctx

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"eve-beemo/src/orchestrator/subjectctx"
	"github.com/lib/pq"
)

func (s *Store) applyPatchDB(sessionID, subjectID string, patch json.RawMessage, ctx RecordContext, attrs ...string) error {
	if s.db == nil || strings.TrimSpace(subjectID) == "" || len(patch) == 0 {
		return nil
	}

	observations, err := s.observationsFromPatch(sessionID, patch, ctx, attrs...)
	if err != nil {
		return err
	}
	if len(observations) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	if err := ensureSubjectRowTx(tx, subjectID, now); err != nil {
		return err
	}

	for _, observation := range observations {
		previous, ok, err := latestObservationTx(tx, subjectID, observation.Attribute)
		if err != nil {
			return err
		}
		if ok && observationsEqual(previous, observation) {
			continue
		}
		if err := insertObservationTx(tx, sessionID, subjectID, observation); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) snapshotDetailsDB(sessionID, subjectID string, attrs ...string) SnapshotDetails {
	if s.db == nil || strings.TrimSpace(subjectID) == "" {
		return SnapshotDetails{}
	}

	query := `
		SELECT session_id, attribute, domain, route, raw_value::text, canonical_value::text, observation_text, embedding_model, source_turn, source_type, created_at
		FROM observations
		WHERE subject_id = $1
	`
	args := []any{subjectID}
	if len(attrs) > 0 {
		query += ` AND attribute = ANY($2)`
		args = append(args, pq.Array(attrs))
	}
	query += ` ORDER BY attribute ASC, created_at DESC, id DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return SnapshotDetails{Err: err}
	}
	defer rows.Close()

	byAttr := map[string][]Observation{}
	for rows.Next() {
		var storedSessionID string
		var attr string
		var domain string
		var route string
		var rawText string
		var canonicalText string
		var observationText string
		var embeddingModel string
		var sourceTurn string
		var sourceType string
		var createdAt time.Time
		if err := rows.Scan(&storedSessionID, &attr, &domain, &route, &rawText, &canonicalText, &observationText, &embeddingModel, &sourceTurn, &sourceType, &createdAt); err != nil {
			return SnapshotDetails{Err: err}
		}
		byAttr[attr] = append(byAttr[attr], Observation{
			SessionID:       storedSessionID,
			Attribute:       attr,
			Domain:          domain,
			Route:           route,
			RawValue:        cloneRaw(json.RawMessage(rawText)),
			CanonicalValue:  cloneRaw(json.RawMessage(canonicalText)),
			ObservationText: observationText,
			EmbeddingModel:  embeddingModel,
			SourceTurn:      sourceTurn,
			SourceType:      sourceType,
			CreatedAt:       createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return SnapshotDetails{Err: err}
	}
	if len(byAttr) == 0 {
		return SnapshotDetails{}
	}

	values := make(map[string]json.RawMessage, len(byAttr))
	conflicts := map[string][]Observation{}
	for attr, observations := range byAttr {
		if value := latestObservationValue(observations); len(value) > 0 {
			values[attr] = value
		}
		if distinct := conflictingExplicitObservations(observations); len(distinct) > 1 {
			conflicts[attr] = distinct
		}
	}
	return SnapshotDetails{
		Values:    values,
		Conflicts: conflicts,
	}
}

func (s *Store) lookupAttributeDB(subjectID, attr string) (Observation, bool, error) {
	if s.db == nil || strings.TrimSpace(subjectID) == "" || strings.TrimSpace(attr) == "" {
		return Observation{}, false, nil
	}

	rows, err := s.db.Query(`
		SELECT session_id, domain, route, raw_value::text, canonical_value::text, observation_text, embedding_model, source_turn, source_type, created_at
		FROM observations
		WHERE subject_id = $1 AND attribute = $2
		ORDER BY created_at DESC, id DESC
	`, subjectID, attr)
	if err != nil {
		return Observation{}, false, err
	}
	defer rows.Close()

	observations := make([]Observation, 0, 4)
	for rows.Next() {
		var sessionID string
		var domain string
		var route string
		var rawText string
		var canonicalText string
		var observationText string
		var embeddingModel string
		var sourceTurn string
		var sourceType string
		var createdAt time.Time
		if err := rows.Scan(&sessionID, &domain, &route, &rawText, &canonicalText, &observationText, &embeddingModel, &sourceTurn, &sourceType, &createdAt); err != nil {
			return Observation{}, false, err
		}
		observations = append(observations, Observation{
			SessionID:       sessionID,
			Attribute:       attr,
			Domain:          domain,
			Route:           route,
			RawValue:        cloneRaw(json.RawMessage(rawText)),
			CanonicalValue:  cloneRaw(json.RawMessage(canonicalText)),
			ObservationText: observationText,
			EmbeddingModel:  embeddingModel,
			SourceTurn:      sourceTurn,
			SourceType:      sourceType,
			CreatedAt:       createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return Observation{}, false, err
	}
	if len(observations) == 0 {
		return Observation{}, false, nil
	}

	ascending := make([]Observation, 0, len(observations))
	for idx := len(observations) - 1; idx >= 0; idx-- {
		ascending = append(ascending, observations[idx])
	}
	return preferredObservationFromAscendingHistory(ascending)
}

func (s *Store) recallDB(subjectID, query string, limit int, timeout time.Duration) ([]RecallMatch, error) {
	if s.db == nil || strings.TrimSpace(subjectID) == "" || strings.TrimSpace(query) == "" || limit <= 0 {
		return nil, nil
	}
	if !s.embeddingEnabled() {
		return nil, nil
	}

	queryVector, err := s.embedQuery(query, timeout)
	if err != nil || len(queryVector) == 0 {
		return nil, err
	}

	rows, err := s.db.Query(`
		SELECT session_id, attribute, domain, route, raw_value::text, canonical_value::text, observation_text, embedding_model, source_turn, source_type, created_at,
		       (1 - (embedding <=> $3::vector)) AS score
		FROM observations
		WHERE subject_id = $1
		  AND embedding_model = $2
		  AND embedding IS NOT NULL
		ORDER BY embedding <=> $3::vector ASC, created_at DESC, id DESC
		LIMIT $4
	`, subjectID, s.model, vectorLiteral(queryVector), limit)
	if err != nil {
		return nil, fmt.Errorf("recall observations: %w", err)
	}
	defer rows.Close()

	matches := make([]RecallMatch, 0, limit)
	for rows.Next() {
		var sessionID string
		var attr string
		var domain string
		var route string
		var rawText string
		var canonicalText string
		var observationText string
		var embeddingModel string
		var sourceTurn string
		var sourceType string
		var createdAt time.Time
		var score float32
		if err := rows.Scan(&sessionID, &attr, &domain, &route, &rawText, &canonicalText, &observationText, &embeddingModel, &sourceTurn, &sourceType, &createdAt, &score); err != nil {
			return nil, fmt.Errorf("scan recalled observation: %w", err)
		}
		matches = append(matches, RecallMatch{
			Observation: Observation{
				SessionID:       sessionID,
				Attribute:       attr,
				Domain:          domain,
				Route:           route,
				RawValue:        cloneRaw(json.RawMessage(rawText)),
				CanonicalValue:  cloneRaw(json.RawMessage(canonicalText)),
				ObservationText: observationText,
				EmbeddingModel:  embeddingModel,
				SourceTurn:      sourceTurn,
				SourceType:      sourceType,
				CreatedAt:       createdAt,
			},
			Score: score,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recalled observations: %w", err)
	}
	return matches, nil
}

func (s *Store) BackfillObservationEmbeddings(timeout time.Duration) (int, error) {
	if s.db == nil || !s.embeddingEnabled() {
		return 0, nil
	}

	rows, err := s.db.Query(`
		SELECT id, session_id, attribute, domain, route, raw_value::text, canonical_value::text, source_turn, source_type, created_at
		FROM observations
		WHERE COALESCE(observation_text, '') = ''
		   OR embedding IS NULL
		   OR COALESCE(embedding_model, '') <> $1
		ORDER BY id ASC
	`, s.model)
	if err != nil {
		return 0, fmt.Errorf("select observation backfill rows: %w", err)
	}
	defer rows.Close()

	type rowData struct {
		id          int64
		observation Observation
	}
	pending := make([]rowData, 0, 64)
	for rows.Next() {
		var id int64
		var sessionID string
		var attr string
		var domain string
		var route string
		var rawText string
		var canonicalText string
		var sourceTurn string
		var sourceType string
		var createdAt time.Time
		if err := rows.Scan(&id, &sessionID, &attr, &domain, &route, &rawText, &canonicalText, &sourceTurn, &sourceType, &createdAt); err != nil {
			return 0, fmt.Errorf("scan observation backfill row: %w", err)
		}
		observation := Observation{
			SessionID:      sessionID,
			Attribute:      attr,
			Domain:         domain,
			Route:          route,
			RawValue:       cloneRaw(json.RawMessage(rawText)),
			CanonicalValue: cloneRaw(json.RawMessage(canonicalText)),
			ObservationText: observationText(attr, json.RawMessage(rawText), json.RawMessage(canonicalText), RecordContext{
				Domain:     domain,
				Route:      route,
				SourceTurn: sourceTurn,
				SourceType: sourceType,
			}),
			SourceTurn: sourceTurn,
			SourceType: sourceType,
			CreatedAt:  createdAt,
		}
		pending = append(pending, rowData{id: id, observation: observation})
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate observation backfill rows: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil
	}

	observations := make([]Observation, 0, len(pending))
	for _, item := range pending {
		observations = append(observations, item.observation)
	}
	s.attachObservationEmbeddings(observations, timeout)

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	for idx, item := range pending {
		observation := observations[idx]
		if len(observation.Embedding) > 0 {
			if _, err := tx.Exec(`
				UPDATE observations
				SET observation_text = $2,
				    embedding_model = $3,
				    embedding = $4::vector
				WHERE id = $1
			`, item.id, observation.ObservationText, observation.EmbeddingModel, vectorLiteral(observation.Embedding)); err != nil {
				return 0, fmt.Errorf("update observation embedding row %d: %w", item.id, err)
			}
			continue
		}
		if _, err := tx.Exec(`
			UPDATE observations
			SET observation_text = $2
			WHERE id = $1
		`, item.id, observation.ObservationText); err != nil {
			return 0, fmt.Errorf("update observation text row %d: %w", item.id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(pending), nil
}

func ensureSubjectRowTx(tx *sql.Tx, subjectID string, now time.Time) error {
	_, err := tx.Exec(`
		INSERT INTO subjects (subject_id, created_at, updated_at)
		VALUES ($1, $2, $2)
		ON CONFLICT (subject_id)
		DO UPDATE SET updated_at = EXCLUDED.updated_at
	`, subjectID, now)
	if err != nil {
		return fmt.Errorf("upsert subject row: %w", err)
	}
	return nil
}

func latestObservationTx(tx *sql.Tx, subjectID, attr string) (Observation, bool, error) {
	row := tx.QueryRow(`
		SELECT session_id, domain, route, raw_value::text, canonical_value::text, observation_text, embedding_model, source_turn, source_type, created_at
		FROM observations
		WHERE subject_id = $1 AND attribute = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, subjectID, attr)

	var sessionID string
	var domain string
	var route string
	var rawText string
	var canonicalText string
	var observationText string
	var embeddingModel string
	var sourceTurn string
	var sourceType string
	var createdAt time.Time
	if err := row.Scan(&sessionID, &domain, &route, &rawText, &canonicalText, &observationText, &embeddingModel, &sourceTurn, &sourceType, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return Observation{}, false, nil
		}
		return Observation{}, false, fmt.Errorf("fetch latest observation: %w", err)
	}

	return Observation{
		SessionID:       sessionID,
		Attribute:       attr,
		Domain:          domain,
		Route:           route,
		RawValue:        cloneRaw(json.RawMessage(rawText)),
		CanonicalValue:  cloneRaw(json.RawMessage(canonicalText)),
		ObservationText: observationText,
		EmbeddingModel:  embeddingModel,
		SourceTurn:      sourceTurn,
		SourceType:      sourceType,
		CreatedAt:       createdAt,
	}, true, nil
}

func insertObservationTx(tx *sql.Tx, sessionID, subjectID string, observation Observation) error {
	args := []any{
		strings.TrimSpace(sessionID),
		subjectID,
		observation.Domain,
		observation.Route,
		observation.Attribute,
		string(observation.RawValue),
		string(observation.CanonicalValue),
		observation.ObservationText,
		observation.EmbeddingModel,
		observation.SourceTurn,
		observation.SourceType,
		observation.CreatedAt,
	}
	query := `
		INSERT INTO observations (
			session_id,
			subject_id,
			domain,
			route,
			attribute,
			raw_value,
			canonical_value,
			observation_text,
			embedding_model,
			source_turn,
			source_type,
			created_at
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11, $12)
	`
	if len(observation.Embedding) > 0 {
		query = `
			INSERT INTO observations (
				session_id,
				subject_id,
				domain,
				route,
				attribute,
				raw_value,
				canonical_value,
				observation_text,
				embedding_model,
				embedding,
				source_turn,
				source_type,
				created_at
			) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10::vector, $11, $12, $13)
		`
		args = []any{
			strings.TrimSpace(sessionID),
			subjectID,
			observation.Domain,
			observation.Route,
			observation.Attribute,
			string(observation.RawValue),
			string(observation.CanonicalValue),
			observation.ObservationText,
			observation.EmbeddingModel,
			vectorLiteral(observation.Embedding),
			observation.SourceTurn,
			observation.SourceType,
			observation.CreatedAt,
		}
	}
	_, err := tx.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("insert observation: %w", err)
	}
	return nil
}

func (s *Store) rememberSubjectAliasesDB(sessionID string, subjects []subjectctx.Subject) error {
	if s.db == nil || len(subjects) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	stmt, err := tx.Prepare(`
		INSERT INTO subject_aliases (subject_id, alias, created_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (subject_id, alias) DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("prepare subject alias insert: %w", err)
	}
	defer stmt.Close()

	for _, subject := range subjects {
		subjectID := strings.TrimSpace(subject.ID)
		if subjectID == "" {
			continue
		}
		if err := ensureSubjectRowTx(tx, subjectID, now); err != nil {
			return err
		}
		for _, alias := range subject.Aliases {
			alias = normalizeAliasForStorage(alias)
			if !shouldPersistAlias(subjectID, alias) {
				continue
			}
			if _, err := stmt.Exec(subjectID, alias, now); err != nil {
				return fmt.Errorf("insert subject alias %s/%s: %w", subjectID, alias, err)
			}
		}
	}

	return tx.Commit()
}

func (s *Store) loadSubjectAliasesDB() ([]subjectctx.Subject, error) {
	if s.db == nil {
		return nil, nil
	}

	rows, err := s.db.Query(`
		SELECT subject_id, alias
		FROM subject_aliases
		ORDER BY subject_id ASC, alias ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("select subject aliases: %w", err)
	}
	defer rows.Close()

	aliasesBySubject := map[string][]string{}
	for rows.Next() {
		var subjectID string
		var alias string
		if err := rows.Scan(&subjectID, &alias); err != nil {
			return nil, fmt.Errorf("scan subject alias: %w", err)
		}
		aliasesBySubject[subjectID] = append(aliasesBySubject[subjectID], alias)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subject aliases: %w", err)
	}
	if len(aliasesBySubject) == 0 {
		return nil, nil
	}

	subjectIDs := make([]string, 0, len(aliasesBySubject))
	for subjectID := range aliasesBySubject {
		subjectIDs = append(subjectIDs, subjectID)
	}
	sort.Strings(subjectIDs)

	subjects := make([]subjectctx.Subject, 0, len(subjectIDs))
	for _, subjectID := range subjectIDs {
		subjects = append(subjects, subjectctx.Subject{
			ID:      subjectID,
			Aliases: append([]string(nil), aliasesBySubject[subjectID]...),
		})
	}
	return subjects, nil
}

func vectorLiteral(values []float32) string {
	if len(values) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for idx, value := range values {
		if idx > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(value), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
