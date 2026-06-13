CREATE TABLE IF NOT EXISTS memory_nodes (
    node_id TEXT PRIMARY KEY,
    node_kind TEXT NOT NULL DEFAULT 'node',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS memory_edges (
    owner_node_id TEXT NOT NULL,
    label TEXT NOT NULL,
    target_node_id TEXT NOT NULL,
    target_kind TEXT NOT NULL DEFAULT 'node',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (owner_node_id, label, target_node_id),
    FOREIGN KEY (owner_node_id) REFERENCES memory_nodes(node_id) ON DELETE CASCADE,
    FOREIGN KEY (target_node_id) REFERENCES memory_nodes(node_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_memory_edges_owner_label
    ON memory_edges (owner_node_id, label);

CREATE INDEX IF NOT EXISTS idx_memory_edges_target
    ON memory_edges (target_node_id);

CREATE TABLE IF NOT EXISTS memory_node_values (
    id BIGSERIAL PRIMARY KEY,
    node_id TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT '',
    domain TEXT NOT NULL DEFAULT '',
    route TEXT NOT NULL DEFAULT '',
    raw_value JSONB NOT NULL,
    canonical_value JSONB NOT NULL,
    observation_text TEXT NOT NULL DEFAULT '',
    embedding_model TEXT NOT NULL DEFAULT '',
    embedding VECTOR,
    source_turn TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (node_id) REFERENCES memory_nodes(node_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_memory_node_values_node_created
    ON memory_node_values (node_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_memory_node_values_embedding_model
    ON memory_node_values (node_id, embedding_model, created_at DESC, id DESC);

INSERT INTO memory_nodes (node_id, node_kind, created_at, updated_at)
SELECT subject_id, 'node', created_at, updated_at
FROM subjects
ON CONFLICT (node_id)
DO UPDATE SET updated_at = EXCLUDED.updated_at;

INSERT INTO memory_nodes (node_id, node_kind)
SELECT DISTINCT
       'node:' || replace(replace(subject_id, ':', '_'), ' ', '_') || ':' || replace(replace(attribute, ':', '_'), ' ', '_'),
       'node'
FROM observations
ON CONFLICT (node_id) DO NOTHING;

INSERT INTO memory_edges (owner_node_id, label, target_node_id, target_kind, updated_at)
SELECT DISTINCT
       subject_id,
       attribute,
       'node:' || replace(replace(subject_id, ':', '_'), ' ', '_') || ':' || replace(replace(attribute, ':', '_'), ' ', '_'),
       'node',
       MAX(created_at)
FROM observations
GROUP BY subject_id, attribute
ON CONFLICT (owner_node_id, label, target_node_id)
DO UPDATE SET updated_at = EXCLUDED.updated_at;

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
)
SELECT
    'node:' || replace(replace(subject_id, ':', '_'), ' ', '_') || ':' || replace(replace(attribute, ':', '_'), ' ', '_'),
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
FROM observations;

INSERT INTO memory_edges (owner_node_id, label, target_node_id, target_kind, updated_at)
SELECT owner_subject_id, relation, target_subject_id, 'node', updated_at
FROM identity_relationships
ON CONFLICT (owner_node_id, label, target_node_id)
DO UPDATE SET updated_at = EXCLUDED.updated_at;
