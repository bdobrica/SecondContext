-- A summary derived from changed evidence cannot retain its old confidence or
-- claims. Invalidate the whole aggregate, including its other evidence links.
CREATE FUNCTION invalidate_memory_derivations() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM person_topic_models
      WHERE user_id=OLD.user_id AND metadata->'evidence_memory_ids' ? OLD.id::text;
    DELETE FROM beliefs WHERE user_id=OLD.user_id AND OLD.id=ANY(evidence_memory_ids);
    DELETE FROM graph_edges WHERE user_id=OLD.user_id AND OLD.id=ANY(evidence_memory_ids);
    RETURN OLD;
END;
$$;
CREATE TRIGGER memory_derivations_deleted AFTER DELETE ON memory_items
    FOR EACH ROW EXECUTE FUNCTION invalidate_memory_derivations();
CREATE TRIGGER memory_derivations_corrected AFTER UPDATE OF summary, people, topics, expires_at ON memory_items
    FOR EACH ROW WHEN (OLD.summary IS DISTINCT FROM NEW.summary OR OLD.people IS DISTINCT FROM NEW.people
      OR OLD.topics IS DISTINCT FROM NEW.topics OR OLD.expires_at IS DISTINCT FROM NEW.expires_at)
    EXECUTE FUNCTION invalidate_memory_derivations();
