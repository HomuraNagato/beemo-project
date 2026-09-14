ALTER TABLE beemo_code.sessions
    ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT 'New session';

UPDATE beemo_code.sessions
SET name = 'Session ' || TO_CHAR(created_at, 'YYYY-MM-DD HH24:MI')
WHERE name = 'New session';

CREATE TABLE IF NOT EXISTS beemo_code.projects (
    user_key TEXT NOT NULL,
    path TEXT NOT NULL,
    name TEXT NOT NULL,
    is_git BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    PRIMARY KEY (user_key, path)
);

CREATE INDEX IF NOT EXISTS beemo_code_projects_user_last_used_idx
    ON beemo_code.projects (user_key, last_used_at DESC NULLS LAST, created_at DESC);
