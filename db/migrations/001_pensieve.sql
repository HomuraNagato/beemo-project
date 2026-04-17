CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS subjects (
    subject_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (subject_id)
);

CREATE TABLE IF NOT EXISTS subject_aliases (
    subject_id TEXT NOT NULL,
    alias TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (subject_id, alias),
    FOREIGN KEY (subject_id) REFERENCES subjects(subject_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS observations (
    id BIGSERIAL PRIMARY KEY,
    session_id TEXT NOT NULL DEFAULT '',
    subject_id TEXT NOT NULL,
    domain TEXT NOT NULL DEFAULT '',
    route TEXT NOT NULL DEFAULT '',
    attribute TEXT NOT NULL,
    raw_value JSONB NOT NULL,
    canonical_value JSONB NOT NULL,
    observation_text TEXT NOT NULL DEFAULT '',
    embedding_model TEXT NOT NULL DEFAULT '',
    embedding VECTOR,
    source_turn TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (subject_id) REFERENCES subjects(subject_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_observations_subject_attr_created
    ON observations (subject_id, attribute, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_observations_subject_route_created
    ON observations (subject_id, route, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_observations_session_subject_created
    ON observations (session_id, subject_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_observations_subject_embedding_model
    ON observations (subject_id, embedding_model, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS route_documents (
    route_id TEXT PRIMARY KEY,
    domain_id TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS route_embeddings (
    route_id TEXT NOT NULL REFERENCES route_documents(route_id) ON DELETE CASCADE,
    model TEXT NOT NULL,
    embedding VECTOR NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (route_id, model)
);
