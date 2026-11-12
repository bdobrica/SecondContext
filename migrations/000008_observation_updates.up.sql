-- Relations preserve both source observations; the working view is derived.
ALTER TABLE memory_items ADD CONSTRAINT memory_items_user_id_id_key UNIQUE(user_id,id);
CREATE TABLE observation_relations (
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 prior_id uuid NOT NULL,
 newer_id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN ('agreement','contradiction','correction','changed_circumstances','outcome_of')),
 prior_quote text NOT NULL,
 new_quote text NOT NULL,
 reason text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,prior_id,newer_id),
 CHECK(prior_id<>newer_id),
 FOREIGN KEY(user_id,prior_id) REFERENCES memory_items(user_id,id) ON DELETE CASCADE,
 FOREIGN KEY(user_id,newer_id) REFERENCES memory_items(user_id,id) ON DELETE CASCADE
);
CREATE INDEX observation_relations_newer ON observation_relations(user_id,newer_id);

-- Explicit structured-model users must not see aggregates derived from a claim
-- that the conversational working view now supersedes or disputes.
CREATE FUNCTION invalidate_related_memory_derivations() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.kind IN ('correction','changed_circumstances','contradiction') THEN
  DELETE FROM person_topic_models WHERE user_id=NEW.user_id
   AND (metadata->'evidence_memory_ids' ? NEW.prior_id::text OR metadata->'evidence_memory_ids' ? NEW.newer_id::text);
  DELETE FROM beliefs WHERE user_id=NEW.user_id AND evidence_memory_ids && ARRAY[NEW.prior_id,NEW.newer_id];
  DELETE FROM graph_edges WHERE user_id=NEW.user_id AND evidence_memory_ids && ARRAY[NEW.prior_id,NEW.newer_id];
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER observation_derivations_changed AFTER INSERT ON observation_relations
 FOR EACH ROW EXECUTE FUNCTION invalidate_related_memory_derivations();

-- Unpublished or fenced evidence never changes the working view. Expiry does
-- not resurrect an explicitly superseded claim: both remain in the inspector.
CREATE VIEW ready_observation_relations AS
 SELECT r.* FROM observation_relations r
 JOIN memory_items p ON p.id=r.prior_id AND p.user_id=r.user_id
 JOIN memory_items n ON n.id=r.newer_id AND n.user_id=r.user_id
 JOIN learning_captures pc ON pc.id=p.capture_id
 JOIN learning_captures nc ON nc.id=n.capture_id
 WHERE p.qdrant_status='completed' AND n.qdrant_status='completed'
 AND (pc.status='completed' OR p.metadata ? 'corrected_at')
 AND (nc.status='completed' OR n.metadata ? 'corrected_at')
 AND NOT EXISTS(SELECT 1 FROM sessions s JOIN context_session_fences f
   ON f.user_id=s.user_id AND f.session_external_id=s.external_id
   WHERE s.id IN (pc.session_id,nc.session_id));

CREATE FUNCTION invalidate_observation_relations() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM observation_relations WHERE user_id=OLD.user_id AND (prior_id=OLD.id OR newer_id=OLD.id);
 RETURN NEW;
END;
$$;
CREATE TRIGGER observation_relations_corrected AFTER UPDATE OF summary,people,topics,expires_at ON memory_items
 FOR EACH ROW WHEN (OLD.summary IS DISTINCT FROM NEW.summary OR OLD.people IS DISTINCT FROM NEW.people
 OR OLD.topics IS DISTINCT FROM NEW.topics OR OLD.expires_at IS DISTINCT FROM NEW.expires_at)
 EXECUTE FUNCTION invalidate_observation_relations();
