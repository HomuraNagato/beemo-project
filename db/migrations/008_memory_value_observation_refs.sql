ALTER TABLE memory_node_values
    ADD COLUMN IF NOT EXISTS observation_id BIGINT REFERENCES observations(id) ON DELETE CASCADE;

ALTER TABLE memory_node_values
    ALTER COLUMN session_id DROP NOT NULL,
    ALTER COLUMN session_id DROP DEFAULT,
    ALTER COLUMN domain DROP NOT NULL,
    ALTER COLUMN domain DROP DEFAULT,
    ALTER COLUMN route DROP NOT NULL,
    ALTER COLUMN route DROP DEFAULT,
    ALTER COLUMN raw_value DROP NOT NULL,
    ALTER COLUMN canonical_value DROP NOT NULL,
    ALTER COLUMN observation_text DROP NOT NULL,
    ALTER COLUMN observation_text DROP DEFAULT,
    ALTER COLUMN embedding_model DROP NOT NULL,
    ALTER COLUMN embedding_model DROP DEFAULT,
    ALTER COLUMN source_turn DROP NOT NULL,
    ALTER COLUMN source_turn DROP DEFAULT,
    ALTER COLUMN source_type DROP NOT NULL,
    ALTER COLUMN source_type DROP DEFAULT;

UPDATE memory_node_values mnv
SET observation_id = o.id
FROM observations o
WHERE mnv.observation_id IS NULL
  AND mnv.node_id = 'node:' || replace(replace(o.subject_id, ':', '_'), ' ', '_') || ':' || replace(replace(o.attribute, ':', '_'), ' ', '_')
  AND mnv.created_at = o.created_at
  AND mnv.session_id = o.session_id
  AND mnv.raw_value = o.raw_value
  AND mnv.canonical_value = o.canonical_value;

CREATE INDEX IF NOT EXISTS idx_memory_node_values_observation
    ON memory_node_values (observation_id)
    WHERE observation_id IS NOT NULL;
