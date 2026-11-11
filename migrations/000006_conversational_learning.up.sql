CREATE TABLE learning_settings (
 user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 enabled boolean NOT NULL DEFAULT false
);

-- source_key identifies an occurrence in supplied history, never just its text.
-- Suppressed/completed receipts survive individual memory deletion.
CREATE TABLE learning_captures (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 session_id uuid REFERENCES sessions(id) ON DELETE CASCADE,
 turn_id uuid,
 source_key text NOT NULL,
 source_text text NOT NULL,
 observed_at timestamptz NOT NULL DEFAULT now(),
 status text NOT NULL CHECK (status IN ('pending','completed','failed','suppressed')),
 observations jsonb,
 attempts integer NOT NULL DEFAULT 0,
 error_code text NOT NULL DEFAULT '',
 retry_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(user_id, source_key),
 FOREIGN KEY(user_id,session_id,turn_id) REFERENCES conversation_turns(user_id,session_id,turn_id) ON DELETE CASCADE
);
CREATE INDEX learning_captures_pending ON learning_captures(retry_at) WHERE status IN ('pending','failed');

ALTER TABLE memory_items ADD COLUMN capture_id uuid REFERENCES learning_captures(id) ON DELETE CASCADE;
