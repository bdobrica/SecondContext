CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    external_id text NOT NULL UNIQUE,
    email text UNIQUE,
    display_name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    external_id text UNIQUE,
    title text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE messages (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('system', 'user', 'assistant', 'tool')),
    content text NOT NULL,
    model text,
    request_id text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE memory_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id uuid REFERENCES sessions(id) ON DELETE SET NULL,
    source_message_id uuid REFERENCES messages(id) ON DELETE SET NULL,
    qdrant_point_id text,
    memory_type text NOT NULL,
    source text NOT NULL DEFAULT 'manual',
    raw_text text NOT NULL,
    summary text NOT NULL,
    people text[] NOT NULL DEFAULT ARRAY[]::text[],
    topics text[] NOT NULL DEFAULT ARRAY[]::text[],
    importance double precision NOT NULL DEFAULT 0 CHECK (importance >= 0 AND importance <= 1),
    utility double precision NOT NULL DEFAULT 0 CHECK (utility >= 0 AND utility <= 1),
    belief_impact double precision NOT NULL DEFAULT 0 CHECK (belief_impact >= 0 AND belief_impact <= 1),
    confidence double precision NOT NULL DEFAULT 0 CHECK (confidence >= 0 AND confidence <= 1),
    expires_at timestamptz,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE people (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    normalized_name text NOT NULL,
    aliases text[] NOT NULL DEFAULT ARRAY[]::text[],
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, normalized_name)
);

CREATE TABLE topics (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    normalized_name text NOT NULL,
    aliases text[] NOT NULL DEFAULT ARRAY[]::text[],
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, normalized_name)
);

CREATE TABLE memory_entities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    memory_item_id uuid NOT NULL REFERENCES memory_items(id) ON DELETE CASCADE,
    entity_type text NOT NULL,
    entity_name text NOT NULL,
    normalized_name text NOT NULL,
    confidence double precision NOT NULL DEFAULT 0 CHECK (confidence >= 0 AND confidence <= 1),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE person_topic_models (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    person_id uuid NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    topic_id uuid NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    niceness double precision NOT NULL DEFAULT 0 CHECK (niceness >= 0 AND niceness <= 1),
    readiness double precision NOT NULL DEFAULT 0 CHECK (readiness >= 0 AND readiness <= 1),
    competence double precision NOT NULL DEFAULT 0 CHECK (competence >= 0 AND competence <= 1),
    capacity double precision NOT NULL DEFAULT 0 CHECK (capacity >= 0 AND capacity <= 1),
    confidence double precision NOT NULL DEFAULT 0 CHECK (confidence >= 0 AND confidence <= 1),
    evidence_count integer NOT NULL DEFAULT 0 CHECK (evidence_count >= 0),
    last_observed_at timestamptz,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, person_id, topic_id)
);

CREATE TABLE beliefs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    topic_id uuid REFERENCES topics(id) ON DELETE SET NULL,
    claim text NOT NULL,
    normalized_claim text NOT NULL,
    stance text NOT NULL CHECK (stance IN ('supports', 'weakens', 'contradicts', 'unknown')),
    confidence double precision NOT NULL DEFAULT 0 CHECK (confidence >= 0 AND confidence <= 1),
    evidence_memory_ids uuid[] NOT NULL DEFAULT ARRAY[]::uuid[],
    last_updated_at timestamptz NOT NULL DEFAULT now(),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE graph_edges (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_kind text NOT NULL,
    source_name text NOT NULL,
    target_kind text NOT NULL,
    target_name text NOT NULL,
    relationship text NOT NULL,
    confidence double precision NOT NULL DEFAULT 0 CHECK (confidence >= 0 AND confidence <= 1),
    evidence_memory_ids uuid[] NOT NULL DEFAULT ARRAY[]::uuid[],
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE interaction_outcomes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id uuid REFERENCES sessions(id) ON DELETE SET NULL,
    message_id uuid REFERENCES messages(id) ON DELETE SET NULL,
    person_id uuid REFERENCES people(id) ON DELETE SET NULL,
    topic_id uuid REFERENCES topics(id) ON DELETE SET NULL,
    goal text,
    predicted_outcome text,
    actual_outcome text NOT NULL,
    success_score double precision NOT NULL DEFAULT 0 CHECK (success_score >= 0 AND success_score <= 1),
    prediction_error text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_sessions_user_id ON sessions (user_id);

CREATE INDEX idx_messages_user_id ON messages (user_id);
CREATE INDEX idx_messages_session_id ON messages (session_id);
CREATE INDEX idx_messages_created_at ON messages (created_at DESC);
CREATE INDEX idx_messages_request_id ON messages (request_id);

CREATE INDEX idx_memory_items_user_id ON memory_items (user_id);
CREATE INDEX idx_memory_items_user_created_at ON memory_items (user_id, created_at DESC);
CREATE INDEX idx_memory_items_memory_type ON memory_items (memory_type);
CREATE INDEX idx_memory_items_expires_at ON memory_items (expires_at);
CREATE INDEX idx_memory_items_people ON memory_items USING gin (people);
CREATE INDEX idx_memory_items_topics ON memory_items USING gin (topics);

CREATE INDEX idx_people_user_id ON people (user_id);
CREATE INDEX idx_people_normalized_name ON people (normalized_name);

CREATE INDEX idx_topics_user_id ON topics (user_id);
CREATE INDEX idx_topics_normalized_name ON topics (normalized_name);

CREATE INDEX idx_memory_entities_memory_item_id ON memory_entities (memory_item_id);
CREATE INDEX idx_memory_entities_type_name ON memory_entities (entity_type, normalized_name);

CREATE INDEX idx_person_topic_models_user_id ON person_topic_models (user_id);
CREATE INDEX idx_person_topic_models_person_id ON person_topic_models (person_id);
CREATE INDEX idx_person_topic_models_topic_id ON person_topic_models (topic_id);
CREATE INDEX idx_person_topic_models_last_observed_at ON person_topic_models (last_observed_at DESC);

CREATE INDEX idx_beliefs_user_id ON beliefs (user_id);
CREATE INDEX idx_beliefs_topic_id ON beliefs (topic_id);
CREATE INDEX idx_beliefs_last_updated_at ON beliefs (last_updated_at DESC);

CREATE INDEX idx_graph_edges_user_id ON graph_edges (user_id);
CREATE INDEX idx_graph_edges_relationship ON graph_edges (relationship);
CREATE INDEX idx_graph_edges_source_target ON graph_edges (source_name, target_name);

CREATE INDEX idx_interaction_outcomes_user_id ON interaction_outcomes (user_id);
CREATE INDEX idx_interaction_outcomes_session_id ON interaction_outcomes (session_id);
CREATE INDEX idx_interaction_outcomes_person_id ON interaction_outcomes (person_id);
CREATE INDEX idx_interaction_outcomes_topic_id ON interaction_outcomes (topic_id);
CREATE INDEX idx_interaction_outcomes_created_at ON interaction_outcomes (created_at DESC);

CREATE TRIGGER users_set_updated_at
BEFORE UPDATE ON users
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER sessions_set_updated_at
BEFORE UPDATE ON sessions
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER memory_items_set_updated_at
BEFORE UPDATE ON memory_items
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER people_set_updated_at
BEFORE UPDATE ON people
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER topics_set_updated_at
BEFORE UPDATE ON topics
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER person_topic_models_set_updated_at
BEFORE UPDATE ON person_topic_models
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER beliefs_set_updated_at
BEFORE UPDATE ON beliefs
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER graph_edges_set_updated_at
BEFORE UPDATE ON graph_edges
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER interaction_outcomes_set_updated_at
BEFORE UPDATE ON interaction_outcomes
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
