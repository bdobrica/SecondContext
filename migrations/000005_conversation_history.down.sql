-- Application rollback keeps migration 5. Dropping terminal fences would permit
-- delayed recreation: intentionally refuse destructive schema rollback.
DO $$ BEGIN RAISE EXCEPTION 'conversation history fences require migration 5; roll back the application without dropping this schema'; END $$;
