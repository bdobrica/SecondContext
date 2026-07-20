ALTER TABLE memory_items
    ADD COLUMN idempotency_key text,
    ADD COLUMN qdrant_status text NOT NULL DEFAULT 'pending'
        CHECK (qdrant_status IN ('pending', 'completed', 'failed')),
    ADD COLUMN person_model_status text NOT NULL DEFAULT 'pending'
        CHECK (person_model_status IN ('pending', 'completed', 'failed')),
    ADD COLUMN belief_status text NOT NULL DEFAULT 'pending'
        CHECK (belief_status IN ('pending', 'completed', 'failed')),
    ADD COLUMN processing_error text;

CREATE UNIQUE INDEX idx_memory_items_user_idempotency
    ON memory_items (user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

ALTER TABLE interaction_outcomes
    ADD COLUMN idempotency_key text,
    ADD COLUMN request_hash text,
    ADD COLUMN memory_id uuid REFERENCES memory_items(id) ON DELETE SET NULL,
    ADD COLUMN processing_status text NOT NULL DEFAULT 'pending'
        CHECK (processing_status IN ('pending', 'completed', 'failed')),
    ADD COLUMN failed_stage text,
    ADD COLUMN processing_error text;

CREATE UNIQUE INDEX idx_interaction_outcomes_user_idempotency
    ON interaction_outcomes (user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE UNIQUE INDEX idx_graph_edges_outcome_identity
    ON graph_edges (
        user_id,
        lower(source_kind),
        lower(source_name),
        lower(target_kind),
        lower(target_name),
        lower(relationship),
        ((metadata ->> 'outcome_id'))
    )
    WHERE metadata ->> 'outcome_id' IS NOT NULL;
