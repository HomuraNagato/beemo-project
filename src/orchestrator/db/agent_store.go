package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

type AgentSession struct {
	SessionID       string
	Name            string
	Mode            string
	Workspace       string
	Status          string
	InternetEnabled bool
	UpdatedAt       time.Time
}

type AgentProject struct {
	Path       string
	Name       string
	IsGit      bool
	CreatedAt  time.Time
	LastUsedAt sql.NullTime
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
		SELECT session_id, name, mode, workspace, status, internet_enabled, updated_at
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
		if err := rows.Scan(&session.SessionID, &session.Name, &session.Mode, &session.Workspace, &session.Status, &session.InternetEnabled, &session.UpdatedAt); err != nil {
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
		SELECT session_id, name, mode, workspace, status, internet_enabled, updated_at
		FROM beemo_code.sessions
		WHERE user_key = $1 AND session_id = $2
	`, userKey, sessionID).Scan(&session.SessionID, &session.Name, &session.Mode, &session.Workspace, &session.Status, &session.InternetEnabled, &session.UpdatedAt)
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

func (s *AgentStore) CreateSession(ctx context.Context, sessionID, userKey string) (AgentSession, error) {
	var session AgentSession
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO beemo_code.sessions (session_id, user_key, name, mode, workspace, status)
		VALUES ($1, $2, 'New session', 'chat', '', 'idle')
		RETURNING session_id, name, mode, workspace, status, internet_enabled, updated_at
	`, sessionID, userKey).Scan(&session.SessionID, &session.Name, &session.Mode, &session.Workspace, &session.Status, &session.InternetEnabled, &session.UpdatedAt)
	return session, err
}

func (s *AgentStore) RenameSession(ctx context.Context, userKey, sessionID, name string) (AgentSession, error) {
	var session AgentSession
	err := s.db.QueryRowContext(ctx, `
		UPDATE beemo_code.sessions SET name = $3, updated_at = NOW()
		WHERE user_key = $1 AND session_id = $2
		RETURNING session_id, name, mode, workspace, status, internet_enabled, updated_at
	`, userKey, sessionID, name).Scan(&session.SessionID, &session.Name, &session.Mode, &session.Workspace, &session.Status, &session.InternetEnabled, &session.UpdatedAt)
	return session, err
}

func (s *AgentStore) DeleteSessions(ctx context.Context, userKey string, sessionIDs []string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM beemo_code.sessions WHERE user_key = $1 AND session_id = ANY($2)
	`, userKey, pq.Array(sessionIDs))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *AgentStore) SetSessionMode(ctx context.Context, userKey, sessionID, mode, workspace string) (AgentSession, error) {
	var session AgentSession
	err := s.db.QueryRowContext(ctx, `
		UPDATE beemo_code.sessions SET mode = $3, workspace = $4, updated_at = NOW()
		WHERE user_key = $1 AND session_id = $2
		RETURNING session_id, name, mode, workspace, status, internet_enabled, updated_at
	`, userKey, sessionID, mode, workspace).Scan(&session.SessionID, &session.Name, &session.Mode, &session.Workspace, &session.Status, &session.InternetEnabled, &session.UpdatedAt)
	return session, err
}

func (s *AgentStore) SetSessionInternet(ctx context.Context, userKey, sessionID string, enabled bool) (AgentSession, error) {
	var session AgentSession
	err := s.db.QueryRowContext(ctx, `
		UPDATE beemo_code.sessions SET internet_enabled = $3, updated_at = NOW()
		WHERE user_key = $1 AND session_id = $2
		RETURNING session_id, name, mode, workspace, status, internet_enabled, updated_at
	`, userKey, sessionID, enabled).Scan(&session.SessionID, &session.Name, &session.Mode, &session.Workspace, &session.Status, &session.InternetEnabled, &session.UpdatedAt)
	return session, err
}

func (s *AgentStore) ListProjects(ctx context.Context, userKey string) ([]AgentProject, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT path, name, is_git, created_at, last_used_at
		FROM beemo_code.projects WHERE user_key = $1
		ORDER BY last_used_at DESC NULLS LAST, created_at, name, path
	`, userKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []AgentProject{}
	for rows.Next() {
		var project AgentProject
		if err := rows.Scan(&project.Path, &project.Name, &project.IsGit, &project.CreatedAt, &project.LastUsedAt); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (s *AgentStore) AddProject(ctx context.Context, userKey, path, name string, isGit bool) (AgentProject, error) {
	var project AgentProject
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO beemo_code.projects (user_key, path, name, is_git)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_key, path) DO UPDATE SET name = EXCLUDED.name, is_git = EXCLUDED.is_git
		RETURNING path, name, is_git, created_at, last_used_at
	`, userKey, path, name, isGit).Scan(&project.Path, &project.Name, &project.IsGit, &project.CreatedAt, &project.LastUsedAt)
	return project, err
}

func (s *AgentStore) RemoveProject(ctx context.Context, userKey, path string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM beemo_code.projects WHERE user_key = $1 AND path = $2`, userKey, path)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

func (s *AgentStore) UseProject(ctx context.Context, userKey, path string) (AgentProject, error) {
	var project AgentProject
	err := s.db.QueryRowContext(ctx, `
		UPDATE beemo_code.projects SET last_used_at = NOW()
		WHERE user_key = $1 AND path = $2
		RETURNING path, name, is_git, created_at, last_used_at
	`, userKey, path).Scan(&project.Path, &project.Name, &project.IsGit, &project.CreatedAt, &project.LastUsedAt)
	return project, err
}
