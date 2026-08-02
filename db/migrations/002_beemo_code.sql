CREATE SCHEMA IF NOT EXISTS beemo_code;

CREATE TABLE IF NOT EXISTS beemo_code.sessions (
    session_id TEXT PRIMARY KEY,
    user_key TEXT NOT NULL,
    mode TEXT NOT NULL DEFAULT 'chat',
    workspace TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS beemo_code_sessions_user_updated_idx
    ON beemo_code.sessions (user_key, updated_at DESC);

CREATE TABLE IF NOT EXISTS beemo_code.events (
    id BIGSERIAL PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES beemo_code.sessions(session_id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    tool TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS beemo_code_events_session_id_idx
    ON beemo_code.events (session_id, id);

CREATE TABLE IF NOT EXISTS beemo_code.approvals (
    approval_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES beemo_code.sessions(session_id) ON DELETE CASCADE,
    action TEXT NOT NULL,
    args JSONB NOT NULL DEFAULT '{}'::JSONB,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS beemo_code_approvals_session_status_idx
    ON beemo_code.approvals (session_id, status);
