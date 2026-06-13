INSERT INTO memory_nodes (node_id, node_kind, created_at, updated_at)
SELECT 'node:' || substring(node_id FROM 6), 'node', created_at, updated_at
FROM memory_nodes
WHERE node_id LIKE 'fact:%'
ON CONFLICT (node_id)
DO UPDATE SET updated_at = GREATEST(memory_nodes.updated_at, EXCLUDED.updated_at);

UPDATE memory_edges
SET target_node_id = 'node:' || substring(target_node_id FROM 6),
    target_kind = 'node'
WHERE target_node_id LIKE 'fact:%';

UPDATE memory_edges
SET owner_node_id = 'node:' || substring(owner_node_id FROM 6)
WHERE owner_node_id LIKE 'fact:%';

UPDATE memory_node_values
SET node_id = 'node:' || substring(node_id FROM 6)
WHERE node_id LIKE 'fact:%';

DELETE FROM memory_nodes
WHERE node_id LIKE 'fact:%';

UPDATE memory_nodes
SET node_kind = 'node';

UPDATE memory_edges
SET target_kind = 'node';

ALTER TABLE memory_edges
    DROP COLUMN IF EXISTS target_kind;

ALTER TABLE memory_nodes
    DROP COLUMN IF EXISTS node_kind;
