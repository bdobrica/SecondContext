ALTER TABLE learning_captures
 ADD COLUMN source_kind text NOT NULL DEFAULT 'conversation'
 CHECK (source_kind IN ('conversation','meeting_note')),
 ADD COLUMN source_metadata jsonb NOT NULL DEFAULT '{}';
