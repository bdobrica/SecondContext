DROP TRIGGER IF EXISTS interaction_outcomes_set_updated_at ON interaction_outcomes;
DROP TRIGGER IF EXISTS graph_edges_set_updated_at ON graph_edges;
DROP TRIGGER IF EXISTS beliefs_set_updated_at ON beliefs;
DROP TRIGGER IF EXISTS person_topic_models_set_updated_at ON person_topic_models;
DROP TRIGGER IF EXISTS topics_set_updated_at ON topics;
DROP TRIGGER IF EXISTS people_set_updated_at ON people;
DROP TRIGGER IF EXISTS memory_items_set_updated_at ON memory_items;
DROP TRIGGER IF EXISTS sessions_set_updated_at ON sessions;
DROP TRIGGER IF EXISTS users_set_updated_at ON users;

DROP TABLE IF EXISTS interaction_outcomes;
DROP TABLE IF EXISTS graph_edges;
DROP TABLE IF EXISTS beliefs;
DROP TABLE IF EXISTS person_topic_models;
DROP TABLE IF EXISTS memory_entities;
DROP TABLE IF EXISTS topics;
DROP TABLE IF EXISTS people;
DROP TABLE IF EXISTS memory_items;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;

DROP FUNCTION IF EXISTS set_updated_at();