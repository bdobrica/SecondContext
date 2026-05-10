# PLAN.md — Context-Augmented LLM MVP

## 1. Purpose

This MVP explores whether a chat interface can be meaningfully improved by adding a persistent cognitive context layer around an LLM.

### Product Description

SecondContext is a context-augmented LLM gateway for recurring work: it stores structured memory about people, topics, prior interactions, and outcomes, retrieves the most relevant context for a current request, and uses that context to produce better answers and communication strategies than a stateless model.

The goal is not to train a new model or replace the LLM's general knowledge. The goal is to augment an existing LLM with user-specific and interaction-specific context, including:

- episodic memory;
- recency, importance, utility, and belief-impact scoring;
- person and topic models;
- social-role context;
- goal-conditioned retrieval;
- scenario generation;
- post-interaction updates.

The first version deliberately avoids LightGBM or any custom predictive model. All extraction, summarization, scenario generation, and updates are handled using LLM calls plus transparent scoring rules.

## 2. MVP Hypothesis

A stateless LLM can answer well in general, but it lacks situated context.

The MVP hypothesis is:

> A chat interface augmented with structured memory, hybrid retrieval, and person/topic models can produce more useful answers and interaction strategies than a stateless LLM, especially for recurring work, stakeholder communication, and decision support.

The MVP should prove that the system can:

1. remember relevant prior information;
2. retrieve it based on the current goal;
3. use it to improve a response;
4. update its internal context after a new interaction;
5. expose enough debug information to understand why it answered the way it did.

## 3. Target Use Case

The first target use case is a personal work assistant for communication and decision support.

Example:

> “Help me ask Alex to review the infrastructure proposal.”

The assistant should retrieve context such as:

- Alex is usually helpful on infrastructure topics;
- Alex dislikes vague requests;
- Alex has limited capacity this week;
- previous interactions with Alex went better when the review scope was narrow;
- the user's goal is to get a useful review without annoying Alex.

The assistant should then generate:

- a recommended message;
- alternative strategies;
- likely response scenarios;
- risks;
- suggested follow-up behavior.

## 3.1 Primary User Persona

The primary user is an individual knowledge worker, technical lead, or founder who repeatedly needs help with stakeholder communication, review requests, prioritization, and decision support across the same people and projects.

## 4. Non-Goals

The MVP does not attempt to:

- train a custom ML model;
- implement LightGBM;
- infer hidden psychological traits with high confidence;
- ingest every possible data source;
- become a full CRM;
- become a general autonomous agent;
- support multi-user enterprise permissions;
- provide production-grade privacy/compliance from day one;
- perfectly model people.

The MVP should remain narrow, inspectable, and easy to debug.

## 5. Core Concepts

### 5.0 Supported Memory Types in v0

The first version should support a small set of explicit memory types:

- interaction observations;
- person preferences;
- project or topic facts;
- belief updates;
- reported outcomes.

This is intentionally narrow. It is enough to support the first communication-assistance demo without turning the system into a generic knowledge ingestion platform.

### 5.1 Memory Item

A memory item is an observed or inferred piece of context.

Examples:

- “Alex asked for a smaller review scope last time.”
- “The migration risk seems higher than expected.”
- “Dana prefers quantified arguments.”
- “The user read an article about vector search tradeoffs.”
- “The Q2 planning discussion changed the user's belief about timeline risk.”

Each memory item should have:

- raw text;
- summary;
- type;
- source;
- timestamp;
- people;
- topics;
- importance score;
- utility score;
- belief-impact score;
- confidence score;
- expiry or decay behavior.

### 5.2 Person/Topic Model

People should not be modeled globally only. The same person may behave differently across topics.

For each person-topic pair, track attributes such as:

- niceness: how likely they are to respond constructively;
- readiness: how quickly or willingly they respond;
- competence: how useful they are on the topic;
- capacity: how much available time or attention they seem to have;
- confidence: how much evidence supports this model;
- evidence count;
- last observed timestamp.

These values are not moral judgments. They are pragmatic, uncertain, topic-specific working estimates.

### 5.3 Belief Model

The system should track claims or beliefs that matter to the user.

A belief can represent:

- a current assumption;
- a hypothesis;
- a changed opinion;
- a conflict between new evidence and prior belief.

Example:

```json
{
  "claim": "The migration project is more risky than originally estimated.",
  "topic": "migration",
  "stance": "supported",
  "confidence": 0.71,
  "last_updated_at": "2026-05-10T12:00:00Z"
}
```

### 5.4 Social Graph

The system should support a graph-like model of entities and relationships.

Example edges:

- user -> Alex: colleague;
- Alex -> Dana: stakeholder;
- Dana -> migration project: approver;
- user -> migration project: owner;
- Alex -> infrastructure: expert.

Edges should include:

- role;
- confidence;
- source memory references;
- timestamps.

### 5.5 Goal-Conditioned Retrieval

Memory retrieval should be driven by the user's current goal.

Example goals:

- get approval;
- request feedback;
- challenge an assumption;
- summarize a topic;
- prepare for a meeting;
- draft a message;
- decide between options.

The same memory may be relevant or irrelevant depending on the goal.

### 5.6 Scenario Generation

For communication tasks, the system should generate several possible strategies and predict likely outcomes.

Example:

- direct request;
- deferential request;
- high-context request;
- minimal low-friction request.

The assistant should recommend the strategy closest to the user's goal while taking risk into account.

### 5.7 Post-Interaction Update

The system should improve after each interaction.

Example user feedback:

> “Alex replied positively but asked me to narrow the scope.”

The system should extract:

- outcome;
- success score;
- new person/topic evidence;
- prediction error;
- memory updates;
- possible belief updates.

This feedback loop is central to the MVP.

## 6. Proposed Architecture

```text
┌──────────────────────────┐
│ Chat client / OpenAI SDK │
└────────────┬─────────────┘
             │ OpenAI-compatible /v1/responses
             ▼
┌──────────────────────────┐
│ Go API Gateway            │
│ - auth                    │
│ - conversation state      │
│ - LLM calls               │
│ - retrieval policy        │
│ - scoring/reranking       │
│ - memory updates          │
└───────┬─────────┬────────┘
        │         │
        │         ▼
        │   ┌──────────────────┐
        │   │ Postgres / KV     │
        │   │ - sessions        │
        │   │ - messages        │
        │   │ - memories        │
        │   │ - people          │
        │   │ - topics          │
        │   │ - beliefs         │
        │   │ - graph edges     │
        │   │ - outcomes        │
        │   └──────────────────┘
        │
        ▼
┌──────────────────────────┐
│ Qdrant                    │
│ - dense vectors           │
│ - sparse/BM25 vectors     │
│ - payload metadata        │
│ - hybrid retrieval        │
└──────────────────────────┘
        │
        ▼
┌──────────────────────────┐
│ Upstream LLM Provider     │
│ - extraction              │
│ - embeddings              │
│ - answer generation       │
│ - scenario simulation     │
│ - memory consolidation    │
└──────────────────────────┘
```

## 7. Main Components

### 7.1 Go API Service

Responsibilities:

- expose OpenAI-compatible endpoints;
- receive chat requests;
- persist messages;
- call extraction prompts;
- call embedding APIs;
- query Qdrant;
- query Postgres;
- rerank memories;
- build augmented prompts;
- call upstream LLM;
- stream or return responses;
- extract memory updates;
- expose debug endpoints.

### 7.2 Qdrant

Qdrant is used as the retrieval index.

It should store:

- dense embeddings;
- sparse/BM25 vectors;
- payload metadata;
- memory references.

Qdrant should not be the source of truth. Canonical memory records should live in Postgres.

### 7.3 Postgres

Postgres stores canonical structured state:

- users;
- sessions;
- messages;
- memory items;
- people;
- topics;
- person-topic models;
- beliefs;
- graph edges;
- interaction outcomes.

For the MVP, Postgres is preferable to a pure key-value store because the data is relational and will need inspection.

### 7.4 LLM Provider

The LLM is used for:

- extracting structured memory from raw text;
- summarizing memory items;
- classifying topics and entities;
- generating dense embeddings;
- generating assistant responses;
- generating scenarios;
- extracting post-interaction outcomes.

The MVP should use prompting and schema validation rather than custom ML.

## 8. OpenAI-Compatible API

The MVP should expose at least:

```text
POST /v1/responses
GET  /v1/models
```

Optionally:

```text
POST /v1/chat/completions
```

A request may look like this:

```json
{
  "model": "context-agent-1",
  "input": "Help me ask Alex to review the infrastructure proposal.",
  "metadata": {
    "goal": "get_review",
    "people": ["Alex"],
    "project": "infrastructure proposal",
    "memory_mode": "social_strategy"
  }
}
```

The system should remain compatible with basic OpenAI-style clients while accepting optional custom metadata.

## 9. Internal API Endpoints

In addition to OpenAI-compatible endpoints, the MVP should expose internal/debug endpoints:

```text
POST /memory/ingest
POST /memory/search
POST /interactions/outcome
GET  /debug/context
GET  /debug/memory/:id
GET  /debug/person/:id
```

The debug endpoints are important for validating retrieval and memory behavior.

## 10. Retrieval Strategy

The retrieval process should use multiple stages.

```text
1. Extract query features:
   - goal
   - people
   - topics
   - entities
   - time hints

2. Query Qdrant:
   - dense semantic search
   - sparse/BM25 search
   - hybrid fusion

3. Apply metadata filters:
   - user_id
   - people
   - topics
   - memory type
   - expiry

4. Rerank in Go:
   - semantic relevance
   - lexical relevance
   - recency
   - importance
   - utility
   - belief impact
   - confidence
   - goal relevance

5. Select top memories:
   - usually 5 to 12
   - avoid redundant memories
   - preserve evidence references

6. Build augmented prompt.
```

A simple MVP scoring formula:

```text
final_score =
  0.35 * hybrid_retrieval_score
+ 0.15 * recency_score
+ 0.15 * importance
+ 0.15 * utility
+ 0.10 * goal_relevance
+ 0.05 * belief_impact
+ 0.05 * confidence
```

This should remain configurable.

## 11. Memory Ingestion Flow

```text
User message or manual note
  -> store raw event
  -> LLM extracts structured memory candidates
  -> validate JSON schema
  -> normalize people/topics/entities
  -> calculate initial scores
  -> create canonical memory records in Postgres
  -> generate dense embedding
  -> generate sparse representation if needed
  -> upsert into Qdrant
```

The MVP should support manual memory ingestion before automatic external ingestion.

## 12. Response Flow

```text
User sends chat request
  -> persist message
  -> extract request features
  -> retrieve memories
  -> retrieve person/topic models
  -> retrieve relevant beliefs and graph edges
  -> assemble context packet
  -> call LLM
  -> return answer
  -> extract memory updates from current turn
  -> persist updates
```

## 13. Outcome Update Flow

```text
User reports outcome
  -> classify actual result
  -> compare with predicted outcome
  -> update interaction_outcomes
  -> create new memory items
  -> update person/topic model
  -> update beliefs if applicable
  -> update graph edges if applicable
```

This is the learning loop of the system.

## 14. Prompting Strategy

Use separate prompts for separate tasks:

1. request feature extraction;
2. memory extraction;
3. person/topic update extraction;
4. scenario generation;
5. answer generation;
6. outcome analysis.

Avoid one giant prompt doing everything.

Each extraction prompt should return strict JSON.

## 15. Observability and Debugging

The MVP should provide visibility into:

- retrieved memories;
- memory scores;
- scoring formula components;
- selected person/topic models;
- selected beliefs;
- generated prompt context;
- LLM extraction outputs;
- failed schema validations;
- memory updates created after a turn.

A useful MVP must be inspectable. Otherwise it will be hard to trust or improve.

## 16. Privacy and Safety Considerations

The MVP should treat person modeling carefully.

Principles:

- store evidence, not just conclusions;
- track confidence;
- allow editing and deletion;
- avoid irreversible judgments;
- avoid sensitive classifications;
- distinguish facts from interpretations;
- expire volatile observations;
- keep models private by default.

The system should not present uncertain personality inferences as facts.

## 17. Success Criteria

The MVP is successful if it can demonstrate:

1. persistent memory across sessions;
2. hybrid retrieval of relevant context;
3. better answers than a stateless LLM for recurring tasks;
4. useful person/topic-aware communication advice;
5. post-interaction updates that affect future responses;
6. transparent debug output;
7. a simple containerized deployment.

A concrete demo scenario:

```text
1. Ingest several memories about Alex, Dana, and a project.
2. Ask the assistant to draft a request to Alex.
3. Show the retrieved context.
4. Show generated strategies.
5. Report Alex's response.
6. Show the updated person/topic model.
7. Ask a similar question later and observe changed behavior.
```

## 18. Recommended MVP Stack

```text
Language: Go
API: chi
Database: Postgres
Vector DB: Qdrant
Migrations: golang-migrate
LLM Provider: OpenAI
Embeddings: text-embedding-3-small
Deployment: Docker Compose
Debug UI: minimal web page or CLI
```

Initial chat model choice for the bootstrap is `gpt-4.1-mini`, with the expectation that model selection remains configurable.

## 19. Suggested Repository Structure

```text
/context-agent
  /cmd
    /api
  /internal
    /api
    /config
    /db
    /llm
    /memory
    /retrieval
    /scoring
    /prompts
    /qdrant
    /models
    /debug
  /migrations
  /deploy
    docker-compose.yml
  /docs
    PLAN.md
    TODO.md
  README.md
```

## 20. MVP Principle

Keep the system simple, explicit, and inspectable.

The first version should prove the loop:

```text
observe -> extract -> store -> retrieve -> reason -> act -> update
```

Do not optimize too early. Do not add a custom predictive model until there is enough outcome data to justify it.
