package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type AgentSession struct {
	SessionID string
	Mode      string
	Workspace string
	Status    string
	UpdatedAt time.Time
}

type AgentEvent struct {
	EventType string
	Tool      string
	Text      string
	Payload   string
	CreatedAt time.Time
}

type AgentStore struct {
	db *sql.DB
}

func NewAgentStore(db *sql.DB) *AgentStore {
	if db == nil {
		return nil
	}
	return &AgentStore{db: db}
}

func (s *AgentStore) UpsertSession(ctx context.Context, sessionID, userKey, mode, workspace, status string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO beemo_code.sessions (session_id, user_key, mode, workspace, status)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (session_id) DO UPDATE SET
			user_key = EXCLUDED.user_key,
			mode = EXCLUDED.mode,
			workspace = EXCLUDED.workspace,
			status = EXCLUDED.status,
			updated_at = NOW()
	`, sessionID, userKey, mode, workspace, status)
	return err
}

func (s *AgentStore) AppendEvent(ctx context.Context, sessionID, eventType, tool, text string, payload any) error {
	if s == nil || s.db == nil {
		return nil
	}
	body := []byte(`{}`)
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = encoded
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO beemo_code.events (session_id, event_type, tool, text, payload)
		VALUES ($1, $2, $3, $4, $5::JSONB)
	`, sessionID, eventType, tool, text, string(body))
	return err
}

func (s *AgentStore) UpdateSessionStatus(ctx context.Context, sessionID, status string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE beemo_code.sessions SET status = $2, updated_at = NOW() WHERE session_id = $1
	`, sessionID, status)
	return err
}

func (s *AgentStore) CreateApproval(ctx context.Context, approvalID, sessionID, action, argsJSON string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if strings.TrimSpace(argsJSON) == "" {
		argsJSON = `{}`
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO beemo_code.approvals (approval_id, session_id, action, args)
		VALUES ($1, $2, $3, $4::JSONB)
		ON CONFLICT (approval_id) DO NOTHING
	`, approvalID, sessionID, action, argsJSON)
	return err
}

func (s *AgentStore) DecideApproval(ctx context.Context, approvalID string, approved bool) error {
	if s == nil || s.db == nil {
		return nil
	}
	status := "denied"
	if approved {
		status = "approved"
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE beemo_code.approvals
		SET status = $2, decided_at = NOW()
		WHERE approval_id = $1 AND status = 'pending'
	`, approvalID, status)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("approval %s is not pending", approvalID)
	}
	return nil
}

func (s *AgentStore) ListSessions(ctx context.Context, userKey string, limit int) ([]AgentSession, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, mode, workspace, status, updated_at
		FROM beemo_code.sessions
		WHERE user_key = $1
		ORDER BY updated_at DESC
		LIMIT $2
	`, userKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AgentSession{}
	for rows.Next() {
		var session AgentSession
		if err := rows.Scan(&session.SessionID, &session.Mode, &session.Workspace, &session.Status, &session.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	return result, rows.Err()
}

func (s *AgentStore) GetSession(ctx context.Context, userKey, sessionID string) (AgentSession, []AgentEvent, error) {
	if s == nil || s.db == nil {
		return AgentSession{}, nil, sql.ErrNoRows
	}
	var session AgentSession
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id, mode, workspace, status, updated_at
		FROM beemo_code.sessions
		WHERE user_key = $1 AND session_id = $2
	`, userKey, sessionID).Scan(&session.SessionID, &session.Mode, &session.Workspace, &session.Status, &session.UpdatedAt)
	if err != nil {
		return AgentSession{}, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_type, tool, text, payload::TEXT, created_at
		FROM beemo_code.events
		WHERE session_id = $1
		ORDER BY id
	`, sessionID)
	if err != nil {
		return AgentSession{}, nil, err
	}
	defer rows.Close()
	events := []AgentEvent{}
	for rows.Next() {
		var event AgentEvent
		if err := rows.Scan(&event.EventType, &event.Tool, &event.Text, &event.Payload, &event.CreatedAt); err != nil {
			return AgentSession{}, nil, err
		}
		events = append(events, event)
	}
	return session, events, rows.Err()
}
