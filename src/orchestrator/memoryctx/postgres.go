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

	return s.insertObservationsDB(sessionID, subjectID, observations)
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
		query += fmt.Sprintf(` AND attribute = ANY($%d)`, len(args)+1)
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

func (s *Store) singleSessionSubjectDB(sessionID string) (string, bool, error) {
	if s.db == nil || strings.TrimSpace(sessionID) == "" {
		return "", false, nil
	}
	rows, err := s.db.Query(`
		SELECT DISTINCT subject_id
		FROM observations
		WHERE session_id = $1 AND subject_id <> 'self'
		ORDER BY subject_id ASC
	`, strings.TrimSpace(sessionID))
	if err != nil {
		return "", false, err
	}
	defer rows.Close()

	var found string
	for rows.Next() {
		var subjectID string
		if err := rows.Scan(&subjectID); err != nil {
			return "", false, err
		}
		subjectID = strings.TrimSpace(subjectID)
		if subjectID == "" {
			continue
		}
		if found != "" && found != subjectID {
			return "", false, nil
		}
		found = subjectID
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	return found, found != "", nil
}

func (s *Store) insertConversationMessageDB(message ConversationMessage) error {
	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`
		INSERT INTO conversation_messages (
			session_id,
			subject_id,
			role,
			content,
			created_at
		) VALUES ($1, $2, $3, $4, $5)
	`, strings.TrimSpace(message.SessionID), strings.TrimSpace(message.SubjectID), strings.TrimSpace(message.Role), strings.TrimSpace(message.Content), message.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert conversation message: %w", err)
	}
	return nil
}

func (s *Store) conversationMessagesDB(sessionID string, limit int) ([]ConversationMessage, error) {
	if s.db == nil || strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT session_id, subject_id, role, content, created_at
		FROM (
			SELECT id, session_id, subject_id, role, content, created_at
			FROM conversation_messages
			WHERE session_id = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2
		) recent
		ORDER BY created_at ASC, id ASC
	`, strings.TrimSpace(sessionID), limit)
	if err != nil {
		return nil, fmt.Errorf("select conversation messages: %w", err)
	}
	defer rows.Close()

	messages := []ConversationMessage{}
	for rows.Next() {
		var message ConversationMessage
		if err := rows.Scan(&message.SessionID, &message.SubjectID, &message.Role, &message.Content, &message.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan conversation message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conversation messages: %w", err)
	}
	return messages, nil
}

func (s *Store) rememberActiveSpeakerDB(sessionID, subjectID string) error {
	if s.db == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(subjectID) == "" {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	if err := ensureSubjectRowTx(tx, strings.TrimSpace(subjectID), now); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO active_speakers (session_id, subject_id, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (session_id)
		DO UPDATE SET subject_id = EXCLUDED.subject_id, updated_at = EXCLUDED.updated_at
	`, strings.TrimSpace(sessionID), strings.TrimSpace(subjectID), now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) activeSpeakerDB(sessionID string) (string, bool, error) {
	if s.db == nil || strings.TrimSpace(sessionID) == "" {
		return "", false, nil
	}
	var subjectID string
	err := s.db.QueryRow(`
		SELECT subject_id
		FROM active_speakers
		WHERE session_id = $1
	`, strings.TrimSpace(sessionID)).Scan(&subjectID)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	subjectID = strings.TrimSpace(subjectID)
	return subjectID, subjectID != "", nil
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

func ensureMemoryNodeRowTx(tx *sql.Tx, nodeID, nodeKind string, now time.Time) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil
	}
	_, err := tx.Exec(`
		INSERT INTO memory_nodes (node_id, created_at, updated_at)
		VALUES ($1, $2, $2)
		ON CONFLICT (node_id)
		DO UPDATE SET updated_at = EXCLUDED.updated_at
	`, nodeID, now)
	if err != nil {
		return fmt.Errorf("upsert memory node: %w", err)
	}
	return nil
}

func insertMemoryEdgeTx(tx *sql.Tx, ownerID, label, targetID string, now time.Time) error {
	ownerID = strings.TrimSpace(ownerID)
	label = normalizeMemoryLabel(label)
	targetID = strings.TrimSpace(targetID)
	if ownerID == "" || label == "" || targetID == "" || ownerID == targetID {
		return nil
	}
	_, err := tx.Exec(`
		INSERT INTO memory_edges (owner_node_id, label, target_node_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (owner_node_id, label, target_node_id)
		DO UPDATE SET updated_at = EXCLUDED.updated_at
	`, ownerID, label, targetID, now)
	if err != nil {
		return fmt.Errorf("insert memory edge %s/%s/%s: %w", ownerID, label, targetID, err)
	}
	return nil
}

func insertObservationGraphTx(tx *sql.Tx, sessionID, subjectID string, observation Observation, observationID int64, now time.Time) error {
	subjectID = strings.TrimSpace(subjectID)
	label := normalizeMemoryLabel(observation.Attribute)
	if subjectID == "" || label == "" {
		return nil
	}
	valueNodeID := memoryValueNodeID(subjectID, label)
	if err := ensureMemoryNodeRowTx(tx, valueNodeID, "node", now); err != nil {
		return err
	}
	if err := insertMemoryEdgeTx(tx, subjectID, label, valueNodeID, now); err != nil {
		return err
	}
	if err := insertMemoryValueTx(tx, sessionID, valueNodeID, observation, observationID); err != nil {
		return err
	}
	return nil
}

func insertMemoryValueTx(tx *sql.Tx, sessionID, nodeID string, observation Observation, observationID int64) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil
	}
	if observationID > 0 {
		if _, err := tx.Exec(`
			INSERT INTO memory_node_values (
				node_id,
				observation_id,
				created_at
			) VALUES ($1, $2, $3)
		`, nodeID, observationID, observation.CreatedAt); err != nil {
			return fmt.Errorf("insert memory node value observation ref: %w", err)
		}
		return nil
	}

	args := []any{
		nodeID,
		strings.TrimSpace(sessionID),
		observation.Domain,
		observation.Route,
		string(observation.RawValue),
		string(observation.CanonicalValue),
		observation.ObservationText,
		observation.EmbeddingModel,
		observation.SourceTurn,
		observation.SourceType,
		observation.CreatedAt,
	}
	query := `
		INSERT INTO memory_node_values (
			node_id,
			session_id,
			domain,
			route,
			raw_value,
			canonical_value,
			observation_text,
			embedding_model,
			source_turn,
			source_type,
			created_at
		) VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7, $8, $9, $10, $11)
	`
	if len(observation.Embedding) > 0 {
		query = `
			INSERT INTO memory_node_values (
				node_id,
				session_id,
				domain,
				route,
				raw_value,
				canonical_value,
				observation_text,
				embedding_model,
				embedding,
				source_turn,
				source_type,
				created_at
			) VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7, $8, $9::vector, $10, $11, $12)
		`
		args = []any{
			nodeID,
			strings.TrimSpace(sessionID),
			observation.Domain,
			observation.Route,
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
	if _, err := tx.Exec(query, args...); err != nil {
		return fmt.Errorf("insert memory node value: %w", err)
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

func insertObservationTx(tx *sql.Tx, sessionID, subjectID string, observation Observation) (int64, error) {
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
		RETURNING id
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
			RETURNING id
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
	var id int64
	if err := tx.QueryRow(query, args...).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert observation: %w", err)
	}
	return id, nil
}

func (s *Store) insertObservationsDB(sessionID, subjectID string, observations []Observation) error {
	if s.db == nil || strings.TrimSpace(subjectID) == "" || len(observations) == 0 {
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
	if err := ensureMemoryNodeRowTx(tx, subjectID, "node", now); err != nil {
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
		observationID, err := insertObservationTx(tx, sessionID, subjectID, observation)
		if err != nil {
			return err
		}
		if err := insertObservationGraphTx(tx, sessionID, subjectID, observation, observationID, now); err != nil {
			return err
		}
	}

	return tx.Commit()
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
		if err := ensureMemoryNodeRowTx(tx, subjectID, "node", now); err != nil {
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

func (s *Store) rememberSubjectRelationshipsDB(relationships []subjectctx.Relationship) error {
	if s.db == nil || len(relationships) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	stmt, err := tx.Prepare(`
		INSERT INTO identity_relationships (owner_subject_id, relation, target_subject_id, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (owner_subject_id, relation, target_subject_id)
		DO UPDATE SET updated_at = EXCLUDED.updated_at
	`)
	if err != nil {
		return fmt.Errorf("prepare identity relationship insert: %w", err)
	}
	defer stmt.Close()

	for _, relationship := range relationships {
		ownerID := strings.TrimSpace(relationship.OwnerID)
		relation := normalizeAliasForStorage(relationship.Relation)
		targetID := strings.TrimSpace(relationship.SubjectID)
		if !shouldPersistRelationship(ownerID, relation, targetID) {
			continue
		}
		if err := ensureSubjectRowTx(tx, ownerID, now); err != nil {
			return err
		}
		if err := ensureSubjectRowTx(tx, targetID, now); err != nil {
			return err
		}
		if err := ensureMemoryNodeRowTx(tx, ownerID, "node", now); err != nil {
			return err
		}
		if err := ensureMemoryNodeRowTx(tx, targetID, "node", now); err != nil {
			return err
		}
		if _, err := stmt.Exec(ownerID, relation, targetID, now); err != nil {
			return fmt.Errorf("insert identity relationship %s/%s/%s: %w", ownerID, relation, targetID, err)
		}
		if err := insertMemoryEdgeTx(tx, ownerID, relation, targetID, now); err != nil {
			return err
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
		alias = normalizeAliasForStorage(alias)
		if !shouldPersistAlias(subjectID, alias) {
			continue
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

func (s *Store) loadSubjectRelationshipsDB() ([]subjectctx.Relationship, error) {
	if s.db == nil {
		return nil, nil
	}

	rows, err := s.db.Query(`
		SELECT owner_subject_id, relation, target_subject_id
		FROM identity_relationships
		ORDER BY owner_subject_id ASC, relation ASC, target_subject_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("select identity relationships: %w", err)
	}
	defer rows.Close()

	relationships := []subjectctx.Relationship{}
	for rows.Next() {
		var ownerID string
		var relation string
		var targetID string
		if err := rows.Scan(&ownerID, &relation, &targetID); err != nil {
			return nil, fmt.Errorf("scan identity relationship: %w", err)
		}
		if !shouldPersistRelationship(ownerID, relation, targetID) {
			continue
		}
		relationships = append(relationships, subjectctx.Relationship{
			OwnerID:   ownerID,
			Relation:  relation,
			SubjectID: targetID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate identity relationships: %w", err)
	}
	return relationships, nil
}

func (s *Store) memoryEdgesDB(ownerID string) ([]MemoryEdge, error) {
	if s.db == nil || strings.TrimSpace(ownerID) == "" {
		return nil, nil
	}

	rows, err := s.db.Query(`
		SELECT owner_node_id, label, target_node_id
		FROM memory_edges
		WHERE owner_node_id = $1
		ORDER BY label ASC, target_node_id ASC
	`, strings.TrimSpace(ownerID))
	if err != nil {
		return nil, fmt.Errorf("select memory edges: %w", err)
	}
	defer rows.Close()

	edges := []MemoryEdge{}
	for rows.Next() {
		var edge MemoryEdge
		if err := rows.Scan(&edge.OwnerID, &edge.Label, &edge.TargetID); err != nil {
			return nil, fmt.Errorf("scan memory edge: %w", err)
		}
		edges = append(edges, edge)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory edges: %w", err)
	}
	return edges, nil
}

func (s *Store) memoryValuesDB(nodeID string) ([]Observation, error) {
	if s.db == nil || strings.TrimSpace(nodeID) == "" {
		return nil, nil
	}

	rows, err := s.db.Query(`
		SELECT
			COALESCE(o.session_id, mnv.session_id, ''),
			COALESCE(o.domain, mnv.domain, ''),
			COALESCE(o.route, mnv.route, ''),
			COALESCE(o.raw_value, mnv.raw_value, 'null'::jsonb)::text,
			COALESCE(o.canonical_value, mnv.canonical_value, 'null'::jsonb)::text,
			COALESCE(o.observation_text, mnv.observation_text, ''),
			COALESCE(o.embedding_model, mnv.embedding_model, ''),
			COALESCE(o.source_turn, mnv.source_turn, ''),
			COALESCE(o.source_type, mnv.source_type, ''),
			COALESCE(o.created_at, mnv.created_at)
		FROM memory_node_values mnv
		LEFT JOIN observations o ON o.id = mnv.observation_id
		WHERE mnv.node_id = $1
		ORDER BY COALESCE(o.created_at, mnv.created_at) DESC, mnv.id DESC
	`, strings.TrimSpace(nodeID))
	if err != nil {
		return nil, fmt.Errorf("select memory node values: %w", err)
	}
	defer rows.Close()

	values := []Observation{}
	for rows.Next() {
		var observation Observation
		var rawText string
		var canonicalText string
		if err := rows.Scan(
			&observation.SessionID,
			&observation.Domain,
			&observation.Route,
			&rawText,
			&canonicalText,
			&observation.ObservationText,
			&observation.EmbeddingModel,
			&observation.SourceTurn,
			&observation.SourceType,
			&observation.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan memory node value: %w", err)
		}
		observation.RawValue = cloneRaw(json.RawMessage(rawText))
		observation.CanonicalValue = cloneRaw(json.RawMessage(canonicalText))
		values = append(values, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory node values: %w", err)
	}
	return values, nil
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
