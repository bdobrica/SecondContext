-- Separate indexed lane: no projection payload is copied into legacy memory/Qdrant.
CREATE TABLE context_projections (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK (kind IN ('entity','event','reading')),
 record_id uuid NOT NULL,
 revision bigint NOT NULL CHECK (revision > 0),
 payload jsonb,
 deleted boolean NOT NULL DEFAULT false,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (user_id,kind,record_id),
 CHECK ((deleted AND payload IS NULL) OR (NOT deleted AND payload IS NOT NULL))
);
CREATE INDEX context_projection_payload ON context_projections USING gin(payload jsonb_path_ops);
CREATE TABLE context_projection_operations (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 operation_id uuid NOT NULL,
 request_hash bytea NOT NULL,
 completed boolean NOT NULL DEFAULT false,
 operation text NOT NULL CHECK (operation IN ('upsert','delete')),
 kind text NOT NULL,
 record_id uuid NOT NULL,
 revision bigint NOT NULL,
 PRIMARY KEY (user_id,operation_id)
);
CREATE TABLE context_projection_sessions (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 session_external_id text NOT NULL,
 kind text NOT NULL,
 record_id uuid NOT NULL,
 PRIMARY KEY (user_id,session_external_id,kind,record_id)
);
