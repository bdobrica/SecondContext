DROP INDEX IF EXISTS idx_graph_edges_outcome_identity;
DROP INDEX IF EXISTS idx_interaction_outcomes_user_idempotency;

ALTER TABLE interaction_outcomes
    DROP COLUMN IF EXISTS processing_error,
    DROP COLUMN IF EXISTS failed_stage,
    DROP COLUMN IF EXISTS processing_status,
    DROP COLUMN IF EXISTS memory_id,
    DROP COLUMN IF EXISTS request_hash,
    DROP COLUMN IF EXISTS idempotency_key;

DROP INDEX IF EXISTS idx_memory_items_user_idempotency;

ALTER TABLE memory_items
    DROP COLUMN IF EXISTS processing_error,
    DROP COLUMN IF EXISTS belief_status,
    DROP COLUMN IF EXISTS person_model_status,
    DROP COLUMN IF EXISTS qdrant_status,
    DROP COLUMN IF EXISTS idempotency_key;
