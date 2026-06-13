CREATE EXTENSION IF NOT EXISTS vector;

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
