# SecondContext — Product implementation checklist

Implemented behavior is documented in [architecture and ADRs](docs/architecture.md),
[operations](docs/operations.md), [demo](docs/demo.md), [evaluation](docs/evaluation.md)
and the [knowledge-base guide](docs/knowledge-base.md). This checklist retains the
development history and remaining work; completed tasks are not a promise that
every broader hardening deliverable is finished.

## Current priority — Conversational learning and product usefulness

Effective 2026-11-11: **useful first, shippable second**. This queue takes precedence
over every older CH, review-remediation, release, integration, and stage checklist
below. See [AGENTS.md](AGENTS.md) for governing guidance and
[PLAN.md](PLAN.md#current-priority--conversational-learning-and-product-usefulness)
for behavior, dependencies, and acceptance demonstrations. PLAN.md and TODO.md
are versioned shared documents; the earlier local-only PLAN convention is superseded.

CL1–CL3 code is implemented, with focused mechanical regression coverage and an
unchanged-client synthetic demonstration of inspection, correction and forgetting.
Real-provider extraction, retrieval and usefulness validation remain open.
Complete the real-model acceptance and comparisons for the first usable milestone M1. No unrelated
production-readiness or rollout checklist blocks that milestone.
Fix concrete blockers and preserve working isolation, acceptance, and forgetting.

| Order | Item | Completion evidence |
| --- | --- | --- |
| 1 | CL1 — Automatic conversational learning | Ordinary chat creates useful source-backed observations without manual ingestion. |
| 2 | CL2 — Natural-language retrieval and strategy | A fresh conversation uses those observations without custom metadata. |
| 3 | CL3 — Memory inspection and correction | The user sees, edits, and forgets memory; subsequent answers follow the changes. |
| 4 | CL4 — Outcomes and belief updates | Natural reports and paraphrased corrections change later advice appropriately. |
| 5 | CL5 — Meeting-note ingestion | Pasted/uploaded notes feed the same inspectable learning loop. |
| 6 | CL6 — Shared bot/workspace knowledge | Distinct customers use shared documents while personal context stays private. |

### CL1 — Automatic conversational learning

- [x] Define one-time learning enablement and separate capture/retrieval controls;
  preserve current `disable_memory` semantics. Pause stops capture; temporary chat
  stops capture and personal-memory retrieval. Keep reference-knowledge controls separate.
- [x] Extend extraction to zero or more useful observations with source/speaker,
  date, time scope/expiry, and explicit-report versus inference labels.
- [x] Add zero-result extraction rules for small talk, unsupported claims, and
  hypothetical examples; exclude assistant drafts from extraction input. Make simple
  observation storage independent of mandatory numeric person/belief/scenario modeling.
- [x] Add background capture to ordinary Chat Completions and Responses and eligible
  managed conversations, without requiring custom turn/session headers for ordinary
  clients. Preserve explicit acceptance for consumers that use it.
- [x] Define eligibility for tool exchanges, incomplete streams, and failed requests;
  never promote rejected/pending assistant drafts into learned facts.
- [x] Reconcile retries and resent transcripts without duplicate observations or
  refreshed evidence dates, while preserving distinct events with identical text.
- [x] Expose recoverable pending/completed/failed capture status and behavior for
  follow-ups before capture completes; keep capture off the answer-streaming path.
- [x] Start the CL3 inspector now: list what was learned with its source and date.
- [x] Verify capture mechanics, no-memory/multiple-result contracts, repeated
  history, acceptance, controls, and subject isolation with focused deterministic
  regression checks.
- [ ] Run real-provider extraction acceptance on natural statements, negative examples,
  and multiple observations; inspect quality rather than counting stored rows. No live
  provider credential was available for this implementation.

Implemented behavior, controls, replay ambiguity, recovery, and inspector usage:
[conversational learning](docs/conversational-learning.md). The automated fresh-chat
check proves changed prompt context after correction/forgetting using a deterministic
provider; it does not complete the real-model M1 usefulness demonstration.

### CL2 — Natural-language retrieval and strategy

- [x] Infer people, topics, and goal from the latest request and relevant bounded
  dialogue; explicit metadata overrides inference and ordinary retrieval is a fallback.
- [x] Resolve supported aliases/references against known entities without merging
  ambiguous people or assigning uncertain observations to them.
- [x] Replace concatenated-all-user-messages retrieval queries with current-task
  queries that retain only the dialogue needed to interpret the request.
- [x] Recall subject-owned memories across fresh conversations without replaying
  unrelated transcripts or requiring a custom session identifier.
- [x] Make advice, alternative strategies, and follow-up drafting reachable through
  natural language on both chat endpoints without `memory_mode`; preserve supported
  answer, tool, and streaming formats.
- [x] Prefer evidence-backed recommendations and tradeoffs over mandatory numeric
  success estimates; expose selected evidence for inspection.
- [x] Demonstrate the Alex example mechanically in an unchanged OpenAI client:
  background capture, fresh Chat/Responses recall, natural follow-ups and a topic
  switch. Deterministic integration checks also cover ambiguous names and aliases.
- [ ] Repeat the demonstration with a real provider and inspect answer quality,
  ambiguous-reference behavior, relevance and latency. No live provider key was
  available; fixture-generated context is not proof of useful advice.

CL2 implementation uses bounded request interpretation, canonical name/alias
checks, current-task retrieval and evidence selection. Ordinary intent stays on
normal generation; explicit metadata retains its overrides. Selected evidence is
available through response headers, Responses context packets and the inspector.
See [behavior, bounds and fallbacks](docs/conversational-learning.md#natural-recall-advice-and-follow-ups-cl2).
The full M1 usefulness milestone remains open.

### CL3 — Memory inspection and correction

- [x] Provide a companion surface for paginated memory list/search, provenance,
  dates, explicit/inferred status, capture/recall progress, and answer evidence.
- [x] Add summary edit, forget, pause-learning, and per-question temporary controls.
  Support explicit natural inspection/correction/forgetting commands on both
  endpoints, with clarification for ambiguous targets and local success receipts.
- [x] Track source dependencies and invalidate retrieved copies and dependent
  person/belief/graph summaries on correction/deletion (migration 000007).
- [x] Preserve occurrence receipts against old-transcript relearning; distinguish
  new reports and explain the scope of client/server-held dialogue.
- [x] Exercise the M1 mechanics with deterministic providers and unchanged SDK:
  statement → capture → fresh recall → correction → changed context → forgetting
  → no future memory context, including old-prefix replay.
- [x] Include pause/temporary chat, delayed capture, another user's isolation,
  repeated transcript, expiry, ambiguity, failed correction and managed acceptance
  in the mechanical checks. Preserve mandatory repository verification.
- [ ] Run the complete M1 demonstration with a real provider and inspect answer
  quality; deterministic prompt echoes do not establish better advice.
- [ ] Compare history alone, simple dated observations, and the full structured
  model with matched inputs/model/knowledge and independent capture/fixtures.
- [ ] Inspect real answer quality and record useful recall, inappropriate capture,
  correction behavior, user effort, and latency that affects use. Start two weeks
  of dogfooding once M1 works; retain findings without claiming unobserved benefits.
- [x] Update implemented behavior/configuration/docs and record the synthetic M1
  demonstration and limits. CL1–CL3/M1 remains open pending real-provider evidence.

Natural controls use a bounded English grammar and current literal phrase/ID
matching; the model cannot authorize mutations. General paraphrased belief updates
remain CL4. Summary edits preserve source text, people/topic labels and expiry.
Managed explicit-acceptance clients inspect but use the companion/ordinary chat
for mutations. No live account was enabled and nothing was deployed.

### CL4 — Natural outcomes and belief updates

Depends on a usable CL1–CL3 loop.

- [ ] Detect naturally reported outcomes and retain dated observations without
  requiring `/interactions/outcome` in normal use.
- [ ] Link outcomes to earlier interactions only with supporting evidence; retain
  ambiguous reports unlinked and never infer a sent action from an assistant draft.
- [ ] Retrieve semantically related beliefs, then classify agreement, contradiction,
  correction, changed circumstances, or unrelated claims before updating them.
- [ ] Preserve source history and current validity without treating semantic
  similarity as proof of identity or a later event as proof a strategy succeeded.
- [ ] Demonstrate changed advice after a natural outcome, paraphrased corrections,
  changing availability, ambiguous earlier requests, and unrelated similar claims.

### CL5 — Meeting-note ingestion

Follows CL4; reuse the proven learning and inspection workflow.

- [ ] Accept pasted/uploaded meeting notes through existing ingestion capabilities;
  let users choose personal observations versus reference material.
- [ ] Reuse source-backed extraction, time/speaker attribution, correction, and
  forgetting; distinguish proposed actions from decisions and completed actions.
- [ ] Demonstrate later useful recall from one real note without manually creating
  memories. Keep calendar synchronization and new connector frameworks deferred.

### CL6 — Shared bot/workspace knowledge

Last in this queue, after CL5. Keep existing private reference retrieval working.

- [ ] Add an explicit shared collection per bot/deployment alongside optional
  private knowledge, reusing the existing knowledge service.
- [ ] Connect upload-once reference material to ordinary answers across different
  users and sessions, with inspectable source attribution.
- [ ] Keep personal memory subject-owned and history conversation-specific;
  never collapse customer identities or automatically publish conversation as shared knowledge.
- [ ] Demonstrate two customers using the same document without private-context
  leakage, and verify that document updates/removal affect subsequent retrieval.

### Deferred work and historical checklist policy

Adaptive-history expansion/rollout, cache optimization, numeric person-model
refinement, probability calibration, graph expansion, broad release preparation,
and unrelated hardening/integration are deferred until usefulness is demonstrated
and priorities are reconsidered. A concrete blocker to CL1–CL6 may be fixed as part
of that slice. Existing required CI checks remain; new production-readiness work
must not displace this queue. This roadmap does not authorize deployment.

Everything below retains original task completion and evidence for reference.
Unchecked historical tasks are not next actions. Their old execution order,
automatic-extraction deferrals, and local-only PLAN rules are superseded above.

## Previous priority — OpenAI-compatible conversation history

CH1–CH3 are complete: contracts, supplied-history Chat Completions and opt-in
managed recent history/finalization. Consumer OCH2–OCH4 are complete. The CH7
recent baseline is published and enabled in `oria-live-test`; live continuity,
erasure and capture rollback passed. Private Telegram review confirmed policy/deletion but failed conversation continuity.
Oria 0.2.3’s private-reading handoff correction is deployed and live synthetic
continuity/topic probes passed. Private Telegram retest remains.
CH4/CH5 observation and cache diagnostics are implemented, disabled by default.
CH6 adaptive selection is implemented, disabled by default; see [adaptive evidence](docs/evidence/conversation-history-ch6.md)
and [observation evidence](docs/evidence/conversation-history-ch45.md). See the
[implemented API matrix](docs/contracts/chat-completions-v1.md).
The accepted [history contract](docs/contracts/conversation-history-v1.md) and
[ADR 0007](docs/adr/0007-conversation-history.md) define the boundary.
This coordinated track adds ordinary Chat Completions support
and optional reliable transcript replay; current stable sessions/semantic memories
do not provide it. Historical execution design is in
[PLAN.md](PLAN.md); companion
[Oria OCH1–OCH5 tasks](https://github.com/bdobrica/OriaEngine/blob/main/TODO.md#current-priority--conversation-history)
cover consent, accepted replies and delivery. Sibling checkout: `../OriaEngine/`.

### CH1 — Freeze ownership and additive API contracts

- [x] Add a new ADR and versioned `conversation-history-1` contract; preserve
  accepted ADRs, existing Responses clients, service identity and deletion fences.
- [x] Specify standard client-supplied dialogue versus opt-in server-managed history;
  never infer a conversation from subject identity or silently combine both sources.
- [x] Define `X-SecondContext-Session`, `-History`, `-Turn`, `-Acceptance`
  headers, existing metadata compatibility, defaults, allowed combinations and errors.
- [x] Define scoped `POST /v1/context/capabilities` and
  `POST /v1/context/turns/finalize`; explicitly extend service authorization for
  those routes and `/v1/chat/completions`, without granting general transcript CRUD.
- [x] Specify immutable turn/request binding, sequence/snapshot, generated versus
  explicit acceptance, exact retry receipts, conflict detection and incomplete-turn recovery.
- [x] Define application-origin prepared local exchanges, source dependencies,
  correction/erasure, receipt retention and the distinction from confirmed delivery.
- [x] Agree schemas and disclosure implications with OCH1; no new behavior is live
  merely because this plan or a health endpoint exists.

### CH2 — Standard OpenAI Chat Completions (depends on CH1)

- [x] Implement `/v1/chat/completions` using the shared pipeline while preserving
  existing `/v1/responses` behavior and `/v1/models` discovery.
- [x] Preserve supplied message ordering/roles and supported tool call/result IDs;
  no custom turn/session/finalize request is required for ordinary clients.
- [x] Implement standard completion and SSE chunk formats, finish reasons, usage,
  error handling, streaming cancellation and incomplete-stream semantics.
- [x] Publish/test the supported parameter, tool, structured-output and modality
  matrix; reject unsupported behavior-affecting fields rather than silently dropping them.
- [x] Verify an unchanged OpenAI SDK client with only endpoint/credential/model
  configuration, including multi-turn chat, streaming, tools and separate chats per subject.

### CH3 — Reliable managed history and finalization (depends on CH1)

- [x] Add migration/repositories for durable turn ordering, request hashes, response
  binding, completion/acceptance, history snapshots and bounded retry receipts.
- [x] Query latest eligible owner/session turns and return chronological groups;
  do not reuse oldest-first `ListBySession` as the recent-history implementation.
- [x] Persist only new inbound content; assembled/replayed context must not duplicate
  transcript rows. Avoid arbitrary text deduplication of legitimate repeated messages.
- [x] Implement capability/finalize endpoints; identical retries converge, conflicting
  reuse fails, and foreign response/session/turn IDs cannot select another account.
- [x] Replay completed generated turns or explicitly finalized accepted replacements
  according to mode; exclude rejected/pending drafts and incomplete streams.
- [x] Define pending-turn barriers, failure recovery and legacy-history rollout;
  never automatically promote old drafts or promise exactly-once provider calls.
- [x] Bound recent turns and whole prompt size, preserving tool groups and reserving
  instructions/current facts/output; evaluate an initial eight-exchange window.
- [x] Enforce deletion/revision fences before classifier/generator access, including
  requests without new knowledge refs; cover all new rows in selective/full erasure.
- [x] Test concurrent ordering, restart, retries, local turns, replacement, limits,
  ownership, delayed finalize after purge and pending linked-session cleanup.

### CH4 — OpenAI Decisions observation (depends on CH3 and working Oria baseline)

- [x] Add bounded `/v1/decisions` HTTP integration, validated typed results, recipe/model
  configuration and provider capability/access checks; keep credentials in SecondContext.
- [x] Evaluate recent-dialogue dependence and older-discussion reference separately
  using prepared current input and bounded eligible history.
- [x] Observe routing while generation still uses recent history; label synthetic
  cases for follow-ups, corrections, topic switches, local exchanges and older references.
- [x] Include the private-reading handoff: horoscope → compatibility → private date
  → plain-English explanation → unrelated topic. Evaluate answer relevance as well
  as prepared-input/replay correctness; never include raw private date answers.
  See [Oria handoff evidence](https://github.com/bdobrica/OriaEngine/blob/main/docs/evidence/reading-focus-handoff.md).
- [x] On classifier uncertainty/refusal/timeout/malformed response, use recent history;
  never substitute that fallback for failed auth, deletion or canonical validation.
- [x] Keep legacy memory salience unchanged; routing decisions confer no authority.

### CH5 — Cache economics and diagnostics (depends on CH4)

- [x] Use stable shared-input prefixes, newest content last, appropriate explicit
  breakpoints and bounded segment rotation; partition opaque keys by subject/session/version.
- [x] Respect the documented 1,024-token minimum and exclusion of question content;
  do not pad requests or assume cache sharing with generation. Recheck current API docs.
- [x] Invalidate eligible content/context generation after correction/erasure; always
  revalidate canonical state and never claim local deletion purges provider caches.
- [x] Measure cache reads/writes, total cost, latency and fallbacks on cold/warm
  short/long conversations; keep transcript text and private IDs out of routine telemetry.

### CH6 — Adaptive selection implemented; production enablement pending

Implementation and paired evaluation: [CH6 evidence](docs/evidence/conversation-history-ch6.md).
Conservative standalone omission, durable retry selections, independent capability/
rollback controls and source/erasure coverage are implemented. No migration is required.
Older-turn retrieval is deferred: evidence does not justify expanding replay.
Cross-session replay, summaries, and transcript embeddings remain deferred under
the original ADR 0007 scope. Its automatic-memory-extraction deferral is superseded
by CL1 above; the implemented contract will be extended with that feature.

- [ ] Before production enablement, establish useful aggregate workload economics and
  quality; the 24-case synthetic comparison preserved context but increased overall
  cost and latency. Selective long-request savings are not an all-traffic benefit.
- [ ] Coordinate a separately configured consumer adaptive opt-in with durable
  header binding. Provider 0.2.3 is published; Oria currently requests recent.

### CH7 — Cross-repository release and evidence (depends on CH2/CH3 and OCH4)

See the [recent-history release checklist](docs/conversation-history-release.md)
and [CH7 evidence](docs/evidence/conversation-history-ch7.md). Gateway
0.2.3 is deployed with Oria 0.2.4 and MCP 0.2.1, using verified immutable digests.
The [CH4–CH6 repeat release](docs/evidence/conversation-history-ch7-repeat.md)
passed publication, live continuity/topic checks, erasure and capture rollback.
Observation and adaptive stay disabled; private Telegram review remains pending.

- [x] Run focused tests, root `make verify`, relevant race tests and Oria contracts/
  replays; record exact checks and limitations in `docs/evidence/`.
- [x] Update architecture, API support matrix, operations, config examples and public
  contracts with each slice; the former local PLAN convention is superseded above.
- [x] Release recent history first; Decisions observation may accompany it, while
  adaptive production enablement retains its separate evaluation/rollout gates. Keep unsupported/deferred capabilities explicit.
- [x] Publish versioned digest-pinned images and coordinate capability/disclosure
  checks and OCH5 rollout; verify migrations and rollback compatibility.
- [x] Verify real-provider generation/continuity and erasure separately from health;
  restrict live changes to `oria-live-test` and keep external `KNOWLEDGE_ENABLED` disabled.
- [x] Verify rollback preserves accepted-turn recovery and deletion in real-gateway
  replay and packaged-image tests; do not treat synthetic checks as a live drill.
- [x] Record the live provider capture rollback drill with generated acceptance,
  local receipt recovery, legacy generation and erasure; preserve all live storage.
- [x] Record the participating user's policy acceptance and account-deletion pass.
- [ ] Retest private Telegram continuity after the Oria private-reading handoff correction.
  Follow the companion OCH5 evidence. No automatic deployment follows from this plan.

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
- [x] Implement `POST /v1/chat/completions` through the CH2 compatibility track.
- [x] Define request/response structs.
- [x] Add upstream LLM client.
- [x] Support non-streaming responses.
- [x] Support Chat Completions streaming through CH2; Responses streaming remains unsupported in the support matrix.
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

- [x] Implement `POST /memory/ingest`.
- [x] Define memory ingestion request schema.
- [x] Store raw text and summary in Postgres.
- [x] Add people and topic fields.
- [x] Add initial scores:
  - [x] importance;
  - [x] utility;
  - [x] belief impact;
  - [x] confidence.
- [x] Generate dense embedding for the memory summary.
- [x] Create Qdrant collection.
- [x] Upsert dense vector into Qdrant.
- [x] Store Qdrant point ID in Postgres.
- [x] Add basic memory listing endpoint.
- [x] Add basic memory deletion endpoint.

### Deliverables

- [x] User can manually ingest a memory.
- [x] Memory is stored in Postgres.
- [x] Memory is searchable in Qdrant by dense vector.

---

## Stage 5 — LLM-Based Memory Extraction

### Goals

- Extract structured memories from raw text using the LLM.
- Avoid manual metadata entry for every memory.

### Tasks

- [x] Create memory extraction prompt.
- [x] Define strict JSON schema for extraction output.
- [x] Extract summary.
- [x] Extract memory type.
- [x] Extract people.
- [x] Extract topics.
- [x] Extract entities.
- [x] Extract importance score.
- [x] Extract utility score.
- [x] Extract belief-impact score.
- [x] Extract confidence score.
- [x] Extract expiry hints.
- [x] Validate LLM JSON output.
- [x] Add retry or repair behavior for invalid JSON.
- [x] Normalize people and topics.
- [x] Store extracted memory in Postgres.
- [x] Index extracted memory in Qdrant.

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

- [x] Raw text can become structured memory.
- [x] Extraction output is validated.
- [x] Extracted memories are retrievable.

---

## Stage 6 — Hybrid Retrieval

### Goals

- Combine semantic search with lexical/sparse search.
- Retrieve memories relevant to the current user request.

### Tasks

- [x] Add sparse/BM25 indexing strategy.
- [x] Configure Qdrant collection for dense and sparse vectors.
- [x] Store sparse representation for memory items.
- [x] Implement hybrid search in Qdrant.
- [x] Add metadata filters:
  - [x] user ID;
  - [x] memory type;
  - [x] people;
  - [x] topics;
  - [x] expiry;
  - [x] confidence threshold.
- [x] Implement `POST /memory/search`.
- [x] Return score breakdown where possible.
- [x] Add retrieval unit tests.

### Deliverables

- [x] User query retrieves relevant memories.
- [x] Hybrid retrieval works better than dense-only for keyword-heavy queries.
- [x] Search results include memory metadata.

---

## Stage 7 — Salience Reranking

### Goals

- Apply product-specific memory ranking after Qdrant retrieval.
- Use recency, importance, utility, belief impact, confidence, and goal relevance.

### Tasks

- [x] Implement recency decay function.
- [x] Implement configurable scoring weights.
- [x] Implement goal relevance scoring.
- [x] Implement confidence filtering.
- [x] Implement redundancy removal.
- [x] Add score breakdown to debug output.
- [x] Add tests for ranking behavior.
- [x] Add config for scoring weights.

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

- [x] Retrieved memories are reranked.
- [x] Debug output explains why each memory was selected.
- [x] Top memories are suitable for prompt injection.

---

## Stage 8 — Prompt Augmentation

### Goals

- Use retrieved context to improve answers.
- Keep prompt construction structured and debuggable.

### Tasks

- [x] Define context packet format.
- [x] Build memory context section.
- [x] Build people context section.
- [x] Build topic context section.
- [x] Build belief context section.
- [x] Build interaction goal section.
- [x] Add prompt template for normal answers.
- [x] Add prompt template for communication advice.
- [x] Add prompt template for scenario generation.
- [x] Add token budget handling.
- [x] Add memory deduplication before prompt injection.
- [x] Persist context packet for debugging.

### Deliverables

- [x] `/v1/responses` uses retrieved memory.
- [x] The assistant can cite or summarize remembered context.
- [x] The system avoids overloading the prompt with too much memory.

---

## Stage 9 — Person and Topic Modeling

### Goals

- Maintain person-topic models based on observed interactions.
- Use them in communication advice.

### Tasks

- [x] Create person/topic extraction prompt.
- [x] Extract person names and aliases.
- [x] Normalize person records.
- [x] Normalize topic records.
- [x] Create or update `person_topic_models`.
- [x] Track:
  - [x] niceness;
  - [x] readiness;
  - [x] competence;
  - [x] capacity;
  - [x] confidence;
  - [x] evidence count;
  - [x] last observed timestamp.
- [x] Add evidence references to memory items.
- [x] Add safe language rules for presenting person models.
- [x] Add endpoint to inspect person model.
- [x] Add endpoint to manually edit person model.

### Deliverables

- [x] System builds topic-specific models of people.
- [x] Communication advice uses person/topic context.
- [x] User can inspect and correct the model.

---

## Stage 10 — Belief and Claim Tracking

### Goals

- Track claims, belief updates, and contradictions.
- Allow belief-impact retrieval.

### Tasks

- [x] Create belief extraction prompt.
- [x] Create `beliefs` repository.
- [x] Extract claims from memory items.
- [x] Track stance:
  - [x] supports;
  - [x] weakens;
  - [x] contradicts;
  - [x] unknown.
- [x] Track confidence.
- [x] Track evidence memory IDs.
- [x] Add belief update behavior.
- [x] Add contradiction detection.
- [x] Add endpoint to inspect beliefs by topic.
- [x] Add belief context to prompt augmentation.

### Deliverables

- [x] System can track belief changes over time.
- [x] System can retrieve belief-relevant memories.
- [x] Assistant can mention uncertainty and contradictory evidence.

---

## Stage 11 — Scenario Generation

### Goals

- Generate multiple possible interaction strategies.
- Pick the strategy most likely to achieve the user’s goal.

### Tasks

- [x] Define supported interaction goals.
- [x] Create scenario generation prompt.
- [x] Generate 3 to 4 strategies.
- [x] For each strategy, produce:
  - [x] message draft;
  - [x] predicted response;
  - [x] benefits;
  - [x] risks;
  - [x] likelihood of success;
  - [x] fallback option.
- [x] Use person/topic model in scenario prompt.
- [x] Add strategy recommendation logic.
- [x] Persist predicted scenario where appropriate.
- [x] Add communication-advice mode to `/v1/responses`.

### Deliverables

- [x] Assistant can generate multiple communication strategies.
- [x] Assistant recommends a strategy based on goal and context.
- [x] Predicted outcomes can later be compared with actual outcomes.

---

## Stage 12 — Outcome Feedback Loop

### Goals

- Let the user report what happened.
- Update memory and person/topic models from the outcome.

### Tasks

- [x] Implement `POST /interactions/outcome`.
- [x] Create outcome extraction prompt.
- [x] Store actual outcome.
- [x] Store success score.
- [x] Compare actual result with predicted result.
- [x] Extract prediction error.
- [x] Create new memory items from outcome.
- [x] Update person/topic model.
- [x] Update beliefs where applicable.
- [x] Update graph edges where applicable.
- [x] Add outcome to future retrieval.
- [x] Add tests for outcome update flow.

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

- [x] User can report interaction outcomes.
- [x] The system updates future behavior based on outcomes.
- [x] Prediction errors are stored for later analysis.

---

## Stage 13 — Debug and Inspection UI

### Goals

- Make the system understandable and debuggable.
- Show why the assistant used certain context.

### Tasks

- [x] Implement `GET /debug/context`.
- [x] Show retrieved memories.
- [x] Show score breakdown.
- [x] Show prompt context packet.
- [x] Show person/topic model used.
- [x] Show relevant beliefs.
- [x] Show scenario predictions.
- [x] Show memory updates from latest turn.
- [x] Build minimal HTML debug page or CLI.
- [x] Add ability to disable memory use for comparison.
- [x] Add ability to compare stateless vs memory-augmented answer.

### Deliverables

- [x] Developer can inspect retrieval behavior.
- [x] Developer can compare with and without memory.
- [x] Debugging does not require database spelunking.

---

## Stage 14 — End-to-End Demo Scenario

### Goals

- Create a compelling demo showing the value of the MVP.

### Tasks

- [x] Define demo characters.
- [x] Define demo topics.
- [x] Seed initial memories.
- [x] Seed person/topic models.
- [x] Seed belief records.
- [x] Ask a communication task.
- [x] Show retrieved memories.
- [x] Generate strategies.
- [x] Pick recommended strategy.
- [x] Submit fake or real outcome.
- [x] Show model update.
- [x] Ask a follow-up task.
- [x] Show changed behavior.

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

- [x] Repeatable demo script.
- [x] Seed data.
- [x] Before/after comparison.
- [ ] Screenshots or recording.

---

## Stage 15 — Evaluation

### Goals

- Measure whether the system improves over a stateless LLM.
- Avoid relying only on subjective impressions.

### Tasks

- [x] Define evaluation prompts.
- [x] Define baseline stateless LLM responses.
- [x] Define memory-augmented responses.
- [x] Compare relevance.
- [x] Compare usefulness.
- [x] Compare personalization.
- [x] Compare communication strategy quality.
- [x] Track retrieval precision manually.
- [x] Track user satisfaction manually.
- [x] Track prediction accuracy over small sample.
- [x] Track whether outcome feedback changes future behavior.

### Simple Metrics

- [x] Retrieval precision@5.
- [x] User-rated answer usefulness.
- [x] User-rated contextual appropriateness.
- [x] Strategy success estimate.
- [x] Actual interaction success score.
- [x] Prediction error notes.
- [x] Number of useful memories created per session.

### Deliverables

- [x] Evaluation dataset.
- [x] Baseline comparison.
- [x] Short evaluation report.

---

## Stage 16 — Hardening

### Goals

- Make the MVP stable enough for repeated use.

### Tasks

- [x] Add authentication.
- [x] Add per-user data isolation.
- [x] Add request rate limits.
- [ ] Add better error messages.
- [ ] Add schema validation everywhere.
- [x] Add mandatory, visible integration tests with pinned dependency services and CI enforcement.
- [x] Add backup/restore notes.
- [x] Add authenticated, scoped subject purge with durable retry/fencing and vector cleanup.
- [x] Add restricted namespace service credentials for multi-user applications.
- [x] Add person model editing (development-only `PUT /debug/person/{id}`).
- [ ] Add belief editing.
- [ ] Add prompt versioning.
- [x] Add model/provider configuration through environment settings.
- [x] Add observability through logs and metrics:
  - [x] logs;
  - [x] metrics;
  - [ ] traces if needed.
- [x] Make interaction-outcome retries idempotent and persist recoverable stage failures.
- [x] Require formatting, vet, unit, tenant-isolation, and cross-store integration checks in CI.

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
- [x] Optional standalone Python service for document processing and reference retrieval.
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

## Knowledge-base follow-ups

The original K1–K10 track is implemented and its planning files have been retired.
Current APIs, lifecycle behavior and limitations are recorded in
[docs/knowledge-base.md](docs/knowledge-base.md). Remaining extensions:

- [ ] OCR for scanned/image-only PDFs.
- [ ] JavaScript-rendered website ingestion with an isolated browser service.
- [ ] Additional office/document formats.
- [ ] Scheduled source refresh policies.
- [ ] Source-level trust/priority controls.
- [ ] Knowledge-owner team sharing/permissions.
- [ ] External object/blob storage for original large files.
- [ ] Incremental document-diff ingestion.
- [ ] Cross-encoder/custom knowledge reranking.
- [ ] Richer derived-knowledge extraction.
- [ ] Source contradiction analysis.

## Linked application context

- [x] Scoped v1 projection upsert/query/delete/capabilities, durable revision and
  operation fences, bounded salience and linked response session erasure.
- [ ] Publish the compatible gateway image and coordinate consumer enablement;
  source tests do not constitute a live deployment check.
