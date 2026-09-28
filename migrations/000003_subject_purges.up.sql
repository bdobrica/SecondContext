-- Retained pseudonymous deletion fence; no messages or profile content.
CREATE TABLE subject_purges (
 external_id text PRIMARY KEY,
 created_at timestamptz NOT NULL DEFAULT now(),
 completed_at timestamptz
);
