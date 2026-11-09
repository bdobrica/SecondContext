-- Accepted history is separate from legacy messages/drafts. No backfill.
ALTER TABLE sessions ADD COLUMN history_sequence bigint NOT NULL DEFAULT 0 CHECK (history_sequence BETWEEN 0 AND 9007199254740991);
ALTER TABLE sessions ADD COLUMN context_generation bigint NOT NULL DEFAULT 1;
CREATE TABLE context_session_fences (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 session_external_id text NOT NULL,
 terminal_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,session_external_id)
);
ALTER TABLE context_projection_sessions ADD COLUMN revision bigint;
UPDATE context_projection_sessions s SET revision=p.revision FROM context_projections p
 WHERE p.user_id=s.user_id AND p.kind=s.kind AND p.record_id=s.record_id;
-- Missing/deleted dependencies from interrupted legacy cleanup remain ineligible.
CREATE TABLE conversation_turns (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 turn_id uuid NOT NULL,
 sequence bigint NOT NULL CHECK(sequence BETWEEN 1 AND 9007199254740991),
 state text NOT NULL CHECK(state IN ('pending','accepted','abandoned')),
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '24 hours',
 terminal_at timestamptz,
 expired boolean NOT NULL DEFAULT false,
 snapshot_sequence bigint,
 context_generation bigint,
 selected_turns uuid[],
 request_hash bytea,
 config_hash bytea,
 acceptance text,
 user_text bytea,
 assistant_text bytea,
 response_id text UNIQUE,
 result bytea,
 finalize_hash bytea,
 receipt jsonb,
 PRIMARY KEY(user_id,session_id,turn_id),
 UNIQUE(session_id,sequence),
 CHECK(state <> 'abandoned' OR (request_hash IS NULL AND config_hash IS NULL AND user_text IS NULL AND assistant_text IS NULL AND result IS NULL AND response_id IS NULL AND finalize_hash IS NULL AND selected_turns IS NULL AND snapshot_sequence IS NULL AND context_generation IS NULL AND acceptance IS NULL))
);
CREATE UNIQUE INDEX conversation_one_pending ON conversation_turns(session_id) WHERE state='pending';
CREATE INDEX conversation_recent ON conversation_turns(user_id,session_id,sequence DESC) WHERE state='accepted';
