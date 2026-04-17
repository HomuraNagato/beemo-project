package memoryctx

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	orchtools "eve-beemo/src/orchestrator/tools"
)

func (s *Store) applyPatchDB(sessionID, subjectID string, patch json.RawMessage, ctx RecordContext, attrs ...string) error {
	if s.db == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(subjectID) == "" || len(patch) == 0 {
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

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	if err := ensureSubjectRowTx(tx, sessionID, subjectID, now); err != nil {
		return err
	}

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
		previous, ok, err := latestObservationTx(tx, sessionID, subjectID, attr)
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
	if s.db == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(subjectID) == "" {
		return SnapshotDetails{}
	}

	query := `
		SELECT attribute, domain, route, raw_value::text, canonical_value::text, source_turn, source_type, created_at
		FROM observations
		WHERE session_id = $1 AND subject_id = $2
	`
	args := []any{sessionID, subjectID}
	if len(attrs) > 0 {
		query += ` AND attribute = ANY($3)`
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
		var attr string
		var domain string
		var route string
		var rawText string
		var canonicalText string
		var sourceTurn string
		var sourceType string
		var createdAt time.Time
		if err := rows.Scan(&attr, &domain, &route, &rawText, &canonicalText, &sourceTurn, &sourceType, &createdAt); err != nil {
			return SnapshotDetails{Err: err}
		}
		byAttr[attr] = append(byAttr[attr], Observation{
			Attribute:      attr,
			Domain:         domain,
			Route:          route,
			RawValue:       cloneRaw(json.RawMessage(rawText)),
			CanonicalValue: cloneRaw(json.RawMessage(canonicalText)),
			SourceTurn:     sourceTurn,
			SourceType:     sourceType,
			CreatedAt:      createdAt,
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

func ensureSubjectRowTx(tx *sql.Tx, sessionID, subjectID string, now time.Time) error {
	_, err := tx.Exec(`
		INSERT INTO subjects (session_id, subject_id, created_at, updated_at)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (session_id, subject_id)
		DO UPDATE SET updated_at = EXCLUDED.updated_at
	`, sessionID, subjectID, now)
	if err != nil {
		return fmt.Errorf("upsert subject row: %w", err)
	}
	return nil
}

func latestObservationTx(tx *sql.Tx, sessionID, subjectID, attr string) (Observation, bool, error) {
	row := tx.QueryRow(`
		SELECT domain, route, raw_value::text, canonical_value::text, source_turn, source_type, created_at
		FROM observations
		WHERE session_id = $1 AND subject_id = $2 AND attribute = $3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, sessionID, subjectID, attr)

	var domain string
	var route string
	var rawText string
	var canonicalText string
	var sourceTurn string
	var sourceType string
	var createdAt time.Time
	if err := row.Scan(&domain, &route, &rawText, &canonicalText, &sourceTurn, &sourceType, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return Observation{}, false, nil
		}
		return Observation{}, false, fmt.Errorf("fetch latest observation: %w", err)
	}

	return Observation{
		Attribute:      attr,
		Domain:         domain,
		Route:          route,
		RawValue:       cloneRaw(json.RawMessage(rawText)),
		CanonicalValue: cloneRaw(json.RawMessage(canonicalText)),
		SourceTurn:     sourceTurn,
		SourceType:     sourceType,
		CreatedAt:      createdAt,
	}, true, nil
}

func insertObservationTx(tx *sql.Tx, sessionID, subjectID string, observation Observation) error {
	_, err := tx.Exec(`
		INSERT INTO observations (
			session_id,
			subject_id,
			domain,
			route,
			attribute,
			raw_value,
			canonical_value,
			source_turn,
			source_type,
			created_at
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10)
	`,
		sessionID,
		subjectID,
		observation.Domain,
		observation.Route,
		observation.Attribute,
		string(observation.RawValue),
		string(observation.CanonicalValue),
		observation.SourceTurn,
		observation.SourceType,
		observation.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert observation: %w", err)
	}
	return nil
}
