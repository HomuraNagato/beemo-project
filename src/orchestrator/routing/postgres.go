package routing

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

func NewSelectorWithDB(path, httpURL, model string, topK, domainTopK int, db *sql.DB) *Selector {
	selector := NewSelector(path, httpURL, model, topK, domainTopK)
	selector.db = db
	return selector
}

func (s *Selector) syncRouteDefinitions(routes []Route, timeout time.Duration) error {
	if s == nil || s.db == nil || len(routes) == 0 {
		return nil
	}

	orderedIDs := make([]string, 0, len(routes))
	descriptorByID := make(map[string]string, len(routes))
	routeByID := make(map[string]Route, len(routes))
	for _, route := range routes {
		routeID := strings.TrimSpace(route.ID)
		if routeID == "" {
			continue
		}
		if _, exists := routeByID[routeID]; exists {
			continue
		}
		routeByID[routeID] = route
		descriptorByID[routeID] = routeDescriptor(route)
		orderedIDs = append(orderedIDs, routeID)
	}
	if len(orderedIDs) == 0 {
		return nil
	}

	existingDocs, err := existingRouteDocumentIDs(s.db, orderedIDs)
	if err != nil {
		return err
	}
	existingEmbeddings, err := existingRouteEmbeddingIDs(s.db, orderedIDs, s.model)
	if err != nil {
		return err
	}

	if err := insertMissingRouteDocuments(s.db, orderedIDs, routeByID, descriptorByID, existingDocs); err != nil {
		return err
	}
	if err := insertMissingRouteEmbeddings(s.db, orderedIDs, descriptorByID, existingEmbeddings, s.model, s.httpURL, timeout, s.embedFn); err != nil {
		return err
	}
	return nil
}

func existingRouteDocumentIDs(db *sql.DB, routeIDs []string) (map[string]struct{}, error) {
	if db == nil || len(routeIDs) == 0 {
		return nil, nil
	}
	rows, err := db.Query(`SELECT route_id FROM route_documents WHERE route_id = ANY($1)`, pq.Array(routeIDs))
	if err != nil {
		return nil, fmt.Errorf("select existing route documents: %w", err)
	}
	defer rows.Close()

	existing := make(map[string]struct{}, len(routeIDs))
	for rows.Next() {
		var routeID string
		if err := rows.Scan(&routeID); err != nil {
			return nil, fmt.Errorf("scan existing route document: %w", err)
		}
		existing[routeID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate existing route documents: %w", err)
	}
	return existing, nil
}

func existingRouteEmbeddingIDs(db *sql.DB, routeIDs []string, model string) (map[string]struct{}, error) {
	if db == nil || len(routeIDs) == 0 || strings.TrimSpace(model) == "" {
		return nil, nil
	}
	rows, err := db.Query(`
		SELECT route_id
		FROM route_embeddings
		WHERE route_id = ANY($1) AND model = $2
	`, pq.Array(routeIDs), strings.TrimSpace(model))
	if err != nil {
		return nil, fmt.Errorf("select existing route embeddings: %w", err)
	}
	defer rows.Close()

	existing := make(map[string]struct{}, len(routeIDs))
	for rows.Next() {
		var routeID string
		if err := rows.Scan(&routeID); err != nil {
			return nil, fmt.Errorf("scan existing route embedding: %w", err)
		}
		existing[routeID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate existing route embeddings: %w", err)
	}
	return existing, nil
}

func insertMissingRouteDocuments(
	db *sql.DB,
	orderedIDs []string,
	routeByID map[string]Route,
	descriptorByID map[string]string,
	existing map[string]struct{},
) error {
	if db == nil || len(orderedIDs) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin route document sync: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO route_documents (route_id, domain_id, title, content, metadata, updated_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, NOW())
		ON CONFLICT (route_id) DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("prepare route document insert: %w", err)
	}
	defer stmt.Close()

	for _, routeID := range orderedIDs {
		if _, ok := existing[routeID]; ok {
			continue
		}
		route := routeByID[routeID]
		metadata, err := json.Marshal(route)
		if err != nil {
			return fmt.Errorf("marshal route metadata %s: %w", routeID, err)
		}
		if _, err := stmt.Exec(routeID, route.Domain, route.Title, descriptorByID[routeID], string(metadata)); err != nil {
			return fmt.Errorf("insert route document %s: %w", routeID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit route document sync: %w", err)
	}
	return nil
}

func insertMissingRouteEmbeddings(
	db *sql.DB,
	orderedIDs []string,
	descriptorByID map[string]string,
	existing map[string]struct{},
	model string,
	httpURL string,
	timeout time.Duration,
	embedFn func(httpURL, model string, inputs []string, timeout time.Duration) ([][]float32, error),
) error {
	model = strings.TrimSpace(model)
	httpURL = strings.TrimSpace(httpURL)
	if db == nil || len(orderedIDs) == 0 || model == "" || httpURL == "" || embedFn == nil {
		return nil
	}

	missingIDs := make([]string, 0, len(orderedIDs))
	inputs := make([]string, 0, len(orderedIDs))
	for _, routeID := range orderedIDs {
		if _, ok := existing[routeID]; ok {
			continue
		}
		descriptor := strings.TrimSpace(descriptorByID[routeID])
		if descriptor == "" {
			continue
		}
		missingIDs = append(missingIDs, routeID)
		inputs = append(inputs, descriptor)
	}
	if len(missingIDs) == 0 {
		return nil
	}

	embeddings, err := embedFn(httpURL, model, inputs, timeout)
	if err != nil {
		return fmt.Errorf("embed missing route documents: %w", err)
	}
	if len(embeddings) != len(missingIDs) {
		return fmt.Errorf("route embedding sync mismatch: got %d want %d", len(embeddings), len(missingIDs))
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin route embedding sync: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO route_embeddings (route_id, model, embedding, updated_at)
		VALUES ($1, $2, $3::vector, NOW())
		ON CONFLICT (route_id, model) DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("prepare route embedding insert: %w", err)
	}
	defer stmt.Close()

	for i, routeID := range missingIDs {
		if _, err := stmt.Exec(routeID, model, formatPGVector(embeddings[i])); err != nil {
			return fmt.Errorf("insert route embedding %s: %w", routeID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit route embedding sync: %w", err)
	}
	return nil
}

func formatPGVector(values []float32) string {
	if len(values) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(value), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
