UPDATE memory_node_values
SET session_id = NULL,
    domain = NULL,
    route = NULL,
    raw_value = NULL,
    canonical_value = NULL,
    observation_text = NULL,
    embedding_model = NULL,
    embedding = NULL,
    source_turn = NULL,
    source_type = NULL
WHERE observation_id IS NOT NULL
  AND (
      session_id IS NOT NULL
      OR domain IS NOT NULL
      OR route IS NOT NULL
      OR raw_value IS NOT NULL
      OR canonical_value IS NOT NULL
      OR observation_text IS NOT NULL
      OR embedding_model IS NOT NULL
      OR embedding IS NOT NULL
      OR source_turn IS NOT NULL
      OR source_type IS NOT NULL
  );
