-- Do not silently misrepresent or erase note captures on downgrade.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM learning_captures WHERE source_kind='meeting_note') THEN
  RAISE EXCEPTION 'Remove meeting-note captures explicitly before downgrading schema 9';
 END IF;
END $$;
ALTER TABLE learning_captures DROP COLUMN source_metadata, DROP COLUMN source_kind;
