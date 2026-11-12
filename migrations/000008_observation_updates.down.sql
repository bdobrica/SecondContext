DROP TRIGGER observation_relations_corrected ON memory_items;
DROP FUNCTION invalidate_observation_relations();
DROP VIEW ready_observation_relations;
DROP TRIGGER observation_derivations_changed ON observation_relations;
DROP FUNCTION invalidate_related_memory_derivations();
DROP TABLE observation_relations;
ALTER TABLE memory_items DROP CONSTRAINT memory_items_user_id_id_key;
