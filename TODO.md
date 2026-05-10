# TODO.md — Work Breakdown Structure for Context-Augmented LLM MVP

## Stage 0 — Project Definition

### Goals

- Define the MVP scope.
- Define the core demo scenario.
- Avoid premature complexity.
- Keep LightGBM and custom ML out of scope for the MVP.

### Tasks

- [x] Write a one-paragraph product description.
- [x] Define the primary user persona.
- [x] Define the first demo use case.
- [x] Define the memory types supported in v0.
- [x] Define what the MVP will not support.
- [x] Choose initial LLM provider.
- [x] Choose initial embedding model.
- [x] Choose Go web framework.
- [x] Choose migration tool.
- [x] Confirm Qdrant + Postgres + Go API architecture.

### Deliverables

- [x] `PLAN.md`
- [x] `TODO.md`
- [x] Initial repository structure
- [x] Initial architecture diagram

---

## Stage 1 — Repository and Local Infrastructure

### Goals

- Create a runnable local development environment.
- Start all required services with Docker Compose.

### Tasks

- [x] Initialize Git repository.
- [x] Create base directory structure.
- [x] Create Go module.
- [x] Add configuration loader.
- [x] Add structured logging.
- [x] Add health check endpoint.
- [x] Add Dockerfile for Go API.
- [x] Add `docker-compose.yml`.
- [x] Add Postgres service.
- [x] Add Qdrant service.
- [x] Add persistent Docker volumes.
- [x] Add `.env.example`.
- [x] Add Makefile or task runner.
- [x] Add README with local startup instructions.

### Suggested Structure

```text
/cmd/api
/internal/api
/internal/config
/internal/db
/internal/llm
/internal/memory
/internal/retrieval
/internal/scoring
/internal/qdrant
/internal/prompts
/migrations
/deploy
/docs
```

### Deliverables

- [x] `docker compose up` starts API, Postgres, and Qdrant.
- [x] `GET /healthz` returns OK.
- [x] Config can be loaded from environment variables.

---

## Stage 2 — Database Schema

### Goals

- Store canonical memory and interaction state in Postgres.
- Keep Qdrant as an index, not the source of truth.

### Tasks

- [x] Add database migration tooling.
- [x] Create `users` table.
- [x] Create `sessions` table.
- [x] Create `messages` table.
- [x] Create `memory_items` table.
- [x] Create `people` table.
- [x] Create `topics` table.
- [x] Create `memory_entities` table.
- [x] Create `person_topic_models` table.
- [x] Create `beliefs` table.
- [x] Create `graph_edges` table.
- [x] Create `interaction_outcomes` table.
- [x] Add indexes for user ID, timestamps, people, topics, and memory type.
- [x] Add seed/dev user support.
- [x] Add basic repository layer in Go.

### Suggested Tables

```text
users
sessions
messages
memory_items
people
topics
memory_entities
person_topic_models
beliefs
graph_edges
interaction_outcomes
```

### Deliverables

- [x] Migrations run successfully.
- [x] Repository layer can create and fetch memory items.
- [x] Repository layer can create and fetch people and topics.
- [x] Repository layer can store conversation messages.

---

## Stage 3 — OpenAI-Compatible API Skeleton

### Goals

- Make the service usable by normal OpenAI-style clients.
- Proxy requests to an upstream LLM before adding memory.

### Tasks

- [x] Implement `GET /v1/models`.
- [x] Implement basic `POST /v1/responses`.
- [ ] Optionally implement `POST /v1/chat/completions`.
- [x] Define request/response structs.
- [x] Add upstream LLM client.
- [x] Support non-streaming responses.
- [ ] Optionally support streaming responses.
- [x] Persist inbound user messages.
- [x] Persist assistant responses.
- [x] Add basic error handling.
- [x] Add request ID logging.

### Deliverables

- [x] A chat client can call the service.
- [x] The service can return a normal LLM response.
- [x] Conversations are persisted to Postgres.

---

## Stage 4 — Manual Memory Ingestion

### Goals

- Add a way to manually create memory items.
- Store memories in Postgres.
- Index memories in Qdrant.

### Tasks

- [ ] Implement `POST /memory/ingest`.
- [ ] Define memory ingestion request schema.
- [ ] Store raw text and summary in Postgres.
- [ ] Add people and topic fields.
- [ ] Add initial scores:
  - [ ] importance;
  - [ ] utility;
  - [ ] belief impact;
  - [ ] confidence.
- [ ] Generate dense embedding for the memory summary.
- [ ] Create Qdrant collection.
- [ ] Upsert dense vector into Qdrant.
- [ ] Store Qdrant point ID in Postgres.
- [ ] Add basic memory listing endpoint.
- [ ] Add basic memory deletion endpoint.

### Deliverables

- [ ] User can manually ingest a memory.
- [ ] Memory is stored in Postgres.
- [ ] Memory is searchable in Qdrant by dense vector.

---

## Stage 5 — LLM-Based Memory Extraction

### Goals

- Extract structured memories from raw text using the LLM.
- Avoid manual metadata entry for every memory.

### Tasks

- [ ] Create memory extraction prompt.
- [ ] Define strict JSON schema for extraction output.
- [ ] Extract summary.
- [ ] Extract memory type.
- [ ] Extract people.
- [ ] Extract topics.
- [ ] Extract entities.
- [ ] Extract importance score.
- [ ] Extract utility score.
- [ ] Extract belief-impact score.
- [ ] Extract confidence score.
- [ ] Extract expiry hints.
- [ ] Validate LLM JSON output.
- [ ] Add retry or repair behavior for invalid JSON.
- [ ] Normalize people and topics.
- [ ] Store extracted memory in Postgres.
- [ ] Index extracted memory in Qdrant.

### Example Extraction Output

```json
{
  "summary": "Alex prefers narrow review scopes for infrastructure proposals.",
  "type": "person_observation",
  "people": ["Alex"],
  "topics": ["infrastructure", "review_process"],
  "importance": 0.67,
  "utility": 0.83,
  "belief_impact": 0.21,
  "confidence": 0.64,
  "expires_in_days": 90
}
```

### Deliverables

- [ ] Raw text can become structured memory.
- [ ] Extraction output is validated.
- [ ] Extracted memories are retrievable.

---

## Stage 6 — Hybrid Retrieval

### Goals

- Combine semantic search with lexical/sparse search.
- Retrieve memories relevant to the current user request.

### Tasks

- [ ] Add sparse/BM25 indexing strategy.
- [ ] Configure Qdrant collection for dense and sparse vectors.
- [ ] Store sparse representation for memory items.
- [ ] Implement hybrid search in Qdrant.
- [ ] Add metadata filters:
  - [ ] user ID;
  - [ ] memory type;
  - [ ] people;
  - [ ] topics;
  - [ ] expiry;
  - [ ] confidence threshold.
- [ ] Implement `POST /memory/search`.
- [ ] Return score breakdown where possible.
- [ ] Add retrieval unit tests.

### Deliverables

- [ ] User query retrieves relevant memories.
- [ ] Hybrid retrieval works better than dense-only for keyword-heavy queries.
- [ ] Search results include memory metadata.

---

## Stage 7 — Salience Reranking

### Goals

- Apply product-specific memory ranking after Qdrant retrieval.
- Use recency, importance, utility, belief impact, confidence, and goal relevance.

### Tasks

- [ ] Implement recency decay function.
- [ ] Implement configurable scoring weights.
- [ ] Implement goal relevance scoring.
- [ ] Implement confidence filtering.
- [ ] Implement redundancy removal.
- [ ] Add score breakdown to debug output.
- [ ] Add tests for ranking behavior.
- [ ] Add config for scoring weights.

### Example Formula

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

### Deliverables

- [ ] Retrieved memories are reranked.
- [ ] Debug output explains why each memory was selected.
- [ ] Top memories are suitable for prompt injection.

---

## Stage 8 — Prompt Augmentation

### Goals

- Use retrieved context to improve answers.
- Keep prompt construction structured and debuggable.

### Tasks

- [ ] Define context packet format.
- [ ] Build memory context section.
- [ ] Build people context section.
- [ ] Build topic context section.
- [ ] Build belief context section.
- [ ] Build interaction goal section.
- [ ] Add prompt template for normal answers.
- [ ] Add prompt template for communication advice.
- [ ] Add prompt template for scenario generation.
- [ ] Add token budget handling.
- [ ] Add memory deduplication before prompt injection.
- [ ] Persist context packet for debugging.

### Deliverables

- [ ] `/v1/responses` uses retrieved memory.
- [ ] The assistant can cite or summarize remembered context.
- [ ] The system avoids overloading the prompt with too much memory.

---

## Stage 9 — Person and Topic Modeling

### Goals

- Maintain person-topic models based on observed interactions.
- Use them in communication advice.

### Tasks

- [ ] Create person/topic extraction prompt.
- [ ] Extract person names and aliases.
- [ ] Normalize person records.
- [ ] Normalize topic records.
- [ ] Create or update `person_topic_models`.
- [ ] Track:
  - [ ] niceness;
  - [ ] readiness;
  - [ ] competence;
  - [ ] capacity;
  - [ ] confidence;
  - [ ] evidence count;
  - [ ] last observed timestamp.
- [ ] Add evidence references to memory items.
- [ ] Add safe language rules for presenting person models.
- [ ] Add endpoint to inspect person model.
- [ ] Add endpoint to manually edit person model.

### Deliverables

- [ ] System builds topic-specific models of people.
- [ ] Communication advice uses person/topic context.
- [ ] User can inspect and correct the model.

---

## Stage 10 — Belief and Claim Tracking

### Goals

- Track claims, belief updates, and contradictions.
- Allow belief-impact retrieval.

### Tasks

- [ ] Create belief extraction prompt.
- [ ] Create `beliefs` repository.
- [ ] Extract claims from memory items.
- [ ] Track stance:
  - [ ] supports;
  - [ ] weakens;
  - [ ] contradicts;
  - [ ] unknown.
- [ ] Track confidence.
- [ ] Track evidence memory IDs.
- [ ] Add belief update behavior.
- [ ] Add contradiction detection.
- [ ] Add endpoint to inspect beliefs by topic.
- [ ] Add belief context to prompt augmentation.

### Deliverables

- [ ] System can track belief changes over time.
- [ ] System can retrieve belief-relevant memories.
- [ ] Assistant can mention uncertainty and contradictory evidence.

---

## Stage 11 — Scenario Generation

### Goals

- Generate multiple possible interaction strategies.
- Pick the strategy most likely to achieve the user’s goal.

### Tasks

- [ ] Define supported interaction goals.
- [ ] Create scenario generation prompt.
- [ ] Generate 3 to 4 strategies.
- [ ] For each strategy, produce:
  - [ ] message draft;
  - [ ] predicted response;
  - [ ] benefits;
  - [ ] risks;
  - [ ] likelihood of success;
  - [ ] fallback option.
- [ ] Use person/topic model in scenario prompt.
- [ ] Add strategy recommendation logic.
- [ ] Persist predicted scenario where appropriate.
- [ ] Add communication-advice mode to `/v1/responses`.

### Deliverables

- [ ] Assistant can generate multiple communication strategies.
- [ ] Assistant recommends a strategy based on goal and context.
- [ ] Predicted outcomes can later be compared with actual outcomes.

---

## Stage 12 — Outcome Feedback Loop

### Goals

- Let the user report what happened.
- Update memory and person/topic models from the outcome.

### Tasks

- [ ] Implement `POST /interactions/outcome`.
- [ ] Create outcome extraction prompt.
- [ ] Store actual outcome.
- [ ] Store success score.
- [ ] Compare actual result with predicted result.
- [ ] Extract prediction error.
- [ ] Create new memory items from outcome.
- [ ] Update person/topic model.
- [ ] Update beliefs where applicable.
- [ ] Update graph edges where applicable.
- [ ] Add outcome to future retrieval.
- [ ] Add tests for outcome update flow.

### Example User Outcome

```text
Alex replied quickly and agreed to review, but asked me to narrow the request to the API section only.
```

### Example Extracted Update

```json
{
  "success_score": 0.78,
  "person_updates": [
    {
      "person": "Alex",
      "topic": "infrastructure_review",
      "readiness_delta": 0.12,
      "capacity_delta": -0.08,
      "new_preference": "Prefers narrow review scopes."
    }
  ],
  "prediction_error": "The system underestimated Alex's need for a tightly scoped request."
}
```

### Deliverables

- [ ] User can report interaction outcomes.
- [ ] The system updates future behavior based on outcomes.
- [ ] Prediction errors are stored for later analysis.

---

## Stage 13 — Debug and Inspection UI

### Goals

- Make the system understandable and debuggable.
- Show why the assistant used certain context.

### Tasks

- [ ] Implement `GET /debug/context`.
- [ ] Show retrieved memories.
- [ ] Show score breakdown.
- [ ] Show prompt context packet.
- [ ] Show person/topic model used.
- [ ] Show relevant beliefs.
- [ ] Show scenario predictions.
- [ ] Show memory updates from latest turn.
- [ ] Build minimal HTML debug page or CLI.
- [ ] Add ability to disable memory use for comparison.
- [ ] Add ability to compare stateless vs memory-augmented answer.

### Deliverables

- [ ] Developer can inspect retrieval behavior.
- [ ] Developer can compare with and without memory.
- [ ] Debugging does not require database spelunking.

---

## Stage 14 — End-to-End Demo Scenario

### Goals

- Create a compelling demo showing the value of the MVP.

### Tasks

- [ ] Define demo characters.
- [ ] Define demo topics.
- [ ] Seed initial memories.
- [ ] Seed person/topic models.
- [ ] Seed belief records.
- [ ] Ask a communication task.
- [ ] Show retrieved memories.
- [ ] Generate strategies.
- [ ] Pick recommended strategy.
- [ ] Submit fake or real outcome.
- [ ] Show model update.
- [ ] Ask a follow-up task.
- [ ] Show changed behavior.

### Suggested Demo

```text
User needs Alex to review an infrastructure proposal.

Existing context:
- Alex is competent on infrastructure.
- Alex dislikes vague requests.
- Alex has low capacity this week.
- Alex previously responded well to short, scoped asks.
- Dana is the approver and wants quantified risk.

The assistant should recommend a narrow, low-friction request to Alex.
```

### Deliverables

- [ ] Repeatable demo script.
- [ ] Seed data.
- [ ] Before/after comparison.
- [ ] Screenshots or recording.

---

## Stage 15 — Evaluation

### Goals

- Measure whether the system improves over a stateless LLM.
- Avoid relying only on subjective impressions.

### Tasks

- [ ] Define evaluation prompts.
- [ ] Define baseline stateless LLM responses.
- [ ] Define memory-augmented responses.
- [ ] Compare relevance.
- [ ] Compare usefulness.
- [ ] Compare personalization.
- [ ] Compare communication strategy quality.
- [ ] Track retrieval precision manually.
- [ ] Track user satisfaction manually.
- [ ] Track prediction accuracy over small sample.
- [ ] Track whether outcome feedback changes future behavior.

### Simple Metrics

- [ ] Retrieval precision@5.
- [ ] User-rated answer usefulness.
- [ ] User-rated contextual appropriateness.
- [ ] Strategy success estimate.
- [ ] Actual interaction success score.
- [ ] Prediction error notes.
- [ ] Number of useful memories created per session.

### Deliverables

- [ ] Evaluation dataset.
- [ ] Baseline comparison.
- [ ] Short evaluation report.

---

## Stage 16 — Hardening

### Goals

- Make the MVP stable enough for repeated use.

### Tasks

- [ ] Add authentication.
- [ ] Add per-user data isolation.
- [ ] Add request rate limits.
- [ ] Add better error messages.
- [ ] Add schema validation everywhere.
- [ ] Add integration tests.
- [ ] Add backup/restore notes.
- [ ] Add memory deletion.
- [ ] Add person model editing.
- [ ] Add belief editing.
- [ ] Add prompt versioning.
- [ ] Add model/provider configuration.
- [ ] Add observability:
  - [ ] logs;
  - [ ] metrics;
  - [ ] traces if needed.

### Deliverables

- [ ] MVP can be used repeatedly without manual database fixes.
- [ ] User can inspect, edit, and delete sensitive memory.
- [ ] System failures are visible and recoverable.

---

## Stage 17 — Optional Next Steps After MVP

These are explicitly post-MVP.

### Possible Extensions

- [ ] Slack ingestion.
- [ ] Gmail ingestion.
- [ ] Calendar ingestion.
- [ ] Browser/article clipping.
- [ ] Meeting transcript ingestion.
- [ ] Knowledge graph visualization.
- [ ] Multi-user team memory.
- [ ] Permissions model.
- [ ] Local embedding model.
- [ ] Python sidecar for document processing.
- [ ] Custom reranker.
- [ ] LightGBM or other predictive model trained on interaction outcomes.
- [ ] Fine-grained role graph.
- [ ] Automated memory consolidation.
- [ ] Memory expiry and archival jobs.
- [ ] Long-term belief evolution timeline.

---

## Suggested Implementation Order

The recommended order is:

```text
1. Infrastructure
2. Database schema
3. OpenAI-compatible proxy
4. Manual memory ingestion
5. Dense retrieval
6. LLM memory extraction
7. Hybrid retrieval
8. Salience reranking
9. Prompt augmentation
10. Person/topic modeling
11. Scenario generation
12. Outcome feedback loop
13. Debug UI
14. Demo script
15. Evaluation
16. Hardening
```

---

## MVP Completion Definition

The MVP is complete when the following scenario works end to end:

```text
1. The user ingests several memories about people, topics, and prior interactions.
2. The user asks for help with a communication or decision task.
3. The system retrieves relevant memories using hybrid search.
4. The system reranks memories using salience scores.
5. The system generates an answer using retrieved context.
6. The system generates multiple possible interaction strategies.
7. The user reports the real outcome.
8. The system updates memory and person/topic models.
9. A later answer changes based on the new context.
10. The debug endpoint shows what context was used and why.
```

No LightGBM or custom predictive model is required for MVP completion.
