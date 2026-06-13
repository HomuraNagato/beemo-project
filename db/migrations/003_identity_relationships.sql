CREATE TABLE IF NOT EXISTS identity_relationships (
    owner_subject_id TEXT NOT NULL,
    relation TEXT NOT NULL,
    target_subject_id TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (owner_subject_id, relation, target_subject_id),
    FOREIGN KEY (owner_subject_id) REFERENCES subjects(subject_id) ON DELETE CASCADE,
    FOREIGN KEY (target_subject_id) REFERENCES subjects(subject_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_identity_relationships_owner_relation
    ON identity_relationships (owner_subject_id, relation);
