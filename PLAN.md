# SecondContext — Implementation Plan

## Current priority — Conversational learning and product usefulness

Decision date: 2026-11-11. Status: **Active; CL1–CL3 implementation available;
real-provider acceptance, matched comparisons and M1 validation pending.**

The next milestone is a useful product that learns from ordinary conversation.
Production readiness follows that demonstrated usefulness. This is a replacement
of the previous priority order, not another task appended behind release work.

This section and [AGENTS.md](AGENTS.md) supersede conflicting priorities, feature
deferrals, and local-only PLAN conventions elsewhere in this repository. Execute
the matching CL1–CL6 tasks in [TODO.md](TODO.md). Existing contracts still describe
implemented behavior; extend them alongside the relevant feature without silently
changing their current meaning. Older ADR deferrals are not a veto on this work.
Historical implementation and verification records remain below for reference.
PLAN.md and TODO.md are shared, committed roadmap documents from this decision onward.

### Product promise and first milestone

Connect an ordinary chat client, enable learning once, converse naturally, and
receive better answers in later conversations because SecondContext remembers
useful context. The user can see, correct, and forget what it learns.

**Milestone M1 comprises CL1, CL2, and CL3 together.** Start a minimal memory
inspector alongside capture; complete correction and forgetting before declaring
the milestone usable. No special memory ingestion endpoint, entity metadata, or
outcome submission is required in the everyday workflow.

The acceptance demonstration is:

1. In ordinary chat, say that Alex prefers small, tightly scoped review requests.
2. After capture completes, open a fresh conversation under the same identity,
   without supplying the old transcript, and ask for help requesting Alex's review.
3. Receive a scoped draft grounded in the learned observation, with inspectable
   evidence showing why that context was selected.
4. Correct the observation and verify that another fresh conversation uses the
   correction rather than the old version.
5. Forget it and verify that it no longer informs personal-memory retrieval or
   derived summaries, including when a client resends the old transcript.
6. Repeat with learning paused and with temporary chat; verify the stated controls
   and that another authenticated user never receives this private observation.

### CL1 — Automatic conversational learning

Priority: first. Dependencies: existing chat and memory capabilities, not unfinished
CH rollout work. Build the minimal CL3 inspection path during this slice.

- Add one-time account/deployment learning enablement and an understandable capture
  status. Support ordinary supplied-history Chat Completions and Responses as well
  as eligible managed conversations; custom turn/session headers are not required
  for the ordinary-client path.
- Extract zero or more useful observations from newly eligible input in the
  background: preferences, constraints, decisions, commitments, project facts,
  and reported events. Skip small talk and unsupported inferences. The existing
  one-record extractor needs an explicit no-memory result and multiple observations.
- Keep source text/reference, speaker, observed date, relevant time period or expiry,
  and the distinction between an explicit report and an inference. Assistant drafts,
  hypotheses, and quoted examples are not evidence that an event occurred.
- Reconcile repeated supplied transcripts and retries without duplicating memories
  or treating repeated old evidence as fresh support. Do not globally collapse
  genuinely distinct events merely because their text matches.
- Reuse existing acceptance boundaries for managed explicit-acceptance consumers;
  rejected/pending drafts must not become learned facts. Specify handling of tools,
  failed requests, and incomplete streams while preserving ordinary client behavior.
- Keep a minimal recoverable capture path and visible pending/completed/failed status;
  capture work must not hold up answer streaming. Define how immediate follow-ups
  behave before background learning completes, without claiming recall is ready early.
- Separate capture from personal-memory retrieval controls. Preserve existing
  `disable_memory` retrieval semantics. Pause learning stops new capture; temporary
  chat disables both capture and personal-memory retrieval. Neither silently changes
  reference-knowledge controls or promises to erase client-held dialogue.
- Store useful observations without requiring every observation to run numerical
  person modeling, belief scoring, or scenario prediction. Keep richer modeling
  separable so its usefulness can be compared later.

Acceptance: natural statements produce the expected source-backed observations;
non-memorable/hypothetical input produces none; repeated history creates no duplicate
support; isolation and capture controls work. This slice alone is not M1 completion.

#### CL1 implementation record — 2026-11-11

Implemented migration 000006, account opt-in/pause, `temporary_chat`, durable
background capture for ordinary Chat/Responses and accepted managed turns,
zero-to-sixteen source-quoted observations, fixed first-observed dates/expiry,
occurrence receipts, retry/status, and `/memory/inspector`. The inspector supports
summary correction and forgetting for the simple conversational path; no numeric
person/belief modeling is required. Source deletion fences/cascades include these
observations and their vector points. Existing manual extraction stays compatible.

Acceptance mechanics are exercised with deterministic provider fixtures: the Alex
statement is captured, a fresh chat receives it, correction changes that prompt,
and forgetting prevents old-prefix replay. Coverage includes no-memory/multiple
result contracts, distinct occurrences, isolation, acceptance, controls, incomplete
streams, restart, partial indexing, expiry, and deletion during capture. This is
not a demonstration of useful real-model advice. No live provider credential was
configured; real extraction quality, the complete M1 demonstration, comparisons,
and dogfooding remain pending. CL2–CL3 are not marked complete.

The ordinary-client identity limit is explicit: identical fresh transcripts without
IDs cannot be distinguished from retries; changed/truncated prefixes can represent
new occurrences. See [implemented behavior and recovery](docs/conversational-learning.md).
No deployment or runtime enablement was performed.

Validation recorded on 2026-11-11: `make verify` passed (formatting, vet, unit tests,
and mandatory Postgres/Qdrant integration tests without skips); `make test-docker`
passed with schema 6; focused CL1 tests passed under `go test -race`. Inspector
JavaScript passed `node --check`; HTTP tests verify the public shell/private API
boundary. Browser interaction/visual checks and live-provider behavior were not
exercised. Test collections are removed after each fixture; isolated test stacks
were used throughout.

### CL2 — Natural-language retrieval and strategy

Priority: second. Depends on CL1 for the full demonstration; retrieval work can use
existing memories while capture is being built.

- Infer people, topics, and immediate goal from the latest request and bounded
  relevant dialogue. Resolve references and supported aliases; leave uncertain
  identities unresolved. Explicit metadata overrides inferred values.
- Focus retrieval on the current question rather than concatenating every old
  user message into the query. Retain ordinary semantic retrieval as a fallback.
- Retrieve personal memories across conversations for the same authenticated
  subject without merging separate transcripts. Do not require a session identifier
  to retrieve subject-owned observations.
- Make natural requests for advice, alternative approaches, and follow-up drafts
  work through ordinary Chat Completions and Responses. Preserve normal answer,
  streaming, and tool formats; users need not supply `memory_mode`.
- Give grounded recommendations and tradeoffs with inspectable context. Numeric
  success probabilities and numeric person traits are not necessary for this path.

Acceptance: an unchanged client completes the Alex example and resolves natural
follow-ups without people/topic/goal/mode metadata. A topic switch uses the new
question, and ambiguous names do not silently acquire another person's observations.

#### CL2 implementation record — 2026-11-11

Implemented bounded current-request interpretation, conservative resolution of
live subject-owned names/stored aliases, focused hybrid retrieval, candidate
selection with inspectable reasons, and natural advice/draft/alternative intent on
both chat endpoints. Ordinary requests require no memory metadata or session ID.
Explicit metadata overrides inference; the ordinary path uses observations without
mandatory numeric person/belief/scenario context. Managed interpretation can use
bounded accepted same-session dialogue; pending and abandoned drafts remain excluded.

Selected memory IDs/status are exposed in additive response headers. Responses
also exposes the context packet. `/memory/evidence` and the companion inspector
resolve IDs to current canonical, subject-owned, unexpired and unfenced records.
No extra copy is stored for inspection; corrections and forgetting take effect.
Managed Responses retains its immutable packet in the existing receipt, while
cached managed Chat receipts do not reconstruct first-generation evidence headers.
The managed recipe hash changes; existing configuration-conflict rules still apply.
No migration beyond 000006, deployment, or live learning enablement was performed.

Deterministic integration fixtures demonstrate fresh Chat/Responses recall,
natural follow-ups/alternatives, topic switches, aliases/shared first names/unknown
names, explicit overrides, fallback on invalid/failed interpretation or selection,
isolation, controls, correction/forgetting and accepted managed history. Tool
messages/structured output and SSE remain supported. An unchanged OpenAI SDK
3.48.0 client performs background capture → fresh recall → follow-ups/topic switch
on both endpoints with only endpoint/credential/model setup; learning is enabled
by its test harness. The fixture echoes assembled context, so this establishes
protocol and evidence flow, not useful real-model advice.

`make verify` passed with mandatory Postgres/Qdrant integration tests and both
unchanged-SDK scripts enabled. Required `make test-docker` packaging checks and
inspector JavaScript `node --check` passed. The added evidence route is covered by
authentication/subject-isolation checks; browser interaction was not exercised.
Disposable test services were cleaned up. The original strategy/debug regressions also pass;
their fixtures now distinguish analysis calls from answer generation. No live
provider key was configured. Real extraction/interpretation/relevance quality,
answer quality, user effort and latency remain unverified. Each request can add
two model calls with four-second deadlines before generation; invalid analysis
falls back to current-text semantic retrieval after identity checks. See
[limits, inspection and recovery](docs/conversational-learning.md). CL2's live
acceptance and the broader CL1–CL3/M1 demonstration remain open.

### CL3 — Memory inspection and correction

Priority: third; the minimum inspector starts with CL1. Completes M1 with CL1–CL2.

- Provide a small companion surface for listing/searching memories with source,
  date, explicit/inferred status, and capture progress. Reuse existing list/delete
  capabilities and add only what the product workflow needs.
- Add edit, forget, pause-learning, and temporary-chat controls. Support natural
  requests to inspect/correct/forget memory, asking for disambiguation when needed.
  Show the memories and documents that informed an answer.
- Track derivation from source observations so edits/deletions update or invalidate
  retrieved copies and person/belief summaries. Prevent immediate relearning of
  forgotten material from a resent old transcript; distinguish a genuinely new report.
- Explain the scope of forgetting: personal memory and its derivatives versus
  dialogue still held and supplied by the client. Do not claim to erase that client.

Acceptance: change a remembered preference, observe changed advice in a fresh
conversation, then forget it and verify effective removal from memory-derived
answers. Run the complete M1 demonstration before marking CL1–CL3 complete.

#### CL3 implementation record — 2026-11-12

The companion inspector now searches all conversational observations through a
paginated canonical endpoint (50/page), showing provenance, correction date,
expiry and recall readiness alongside capture progress. Its fresh-question form
has a per-question temporary control and displays the selected personal memories,
selection reasons and reference excerpts supplied to that answer. Credentials and
rendered results stay in page memory and clear on disconnect.

Ordinary Chat/Responses accept explicit natural inspection, summary correction,
forgetting and pause/resume commands. A literal phrase or memory ID selects the
target; multiple matches require clarification. Local receipts report operations
without allowing generated text or historical/tool messages to authorize writes.
Chat streaming and unchanged SDK parsing remain supported. Commands never become
new learned observations. Disabled/temporary personal memory blocks commands.
Managed generated turns retain retry receipts; explicit-acceptance drafts direct
mutations to the companion/ordinary chat, and acceptance does not execute them.

Correction retains original source/labels/expiry and adds its own date to retrieval
context. Migration 000007 atomically invalidates source-linked person/topic models,
beliefs and graph edges on correction or deletion, including cascaded source
removal. The whole dependent aggregate is invalidated; independent aggregates
survive. Existing occurrence receipts continue to prevent matching old-prefix
relearning. Client/server-held dialogue remains outside individual-memory erasure.
The implementation does not infer missing dependencies for source-free manual
model values or implement CL4's general paraphrased belief updates.

The deterministic M1 sequence passed on both endpoints: natural statement,
background capture, fresh recall, natural correction, changed fresh-answer context,
forgetting, no later personal-memory context, and repeated-old-prefix suppression.
The unchanged OpenAI SDK also completed inspection, correction, fresh recall,
pause via SSE and forgetting. Tests cover ambiguity, subject isolation, expiry and
pagination, correction index failure, dependency invalidation, managed retries and
explicit acceptance; existing CL1 controls/delayed-capture checks remain mandatory.
`make verify` passed, including fmt/vet/unit and mandatory Postgres/Qdrant integration
checks with both unchanged-SDK scripts. `make test-docker` passed on schema 7;
inspector JavaScript syntax and source/documentation review passed. Disposable
test stacks were cleaned up.

No real provider credential was available. Fixture generators echo their prompts;
changed context is evidence of wiring, not improved advice. Live extraction/answer
quality, matched history/observation/structured-model comparisons, measured user
effort/latency and two weeks of dogfooding remain open. No browser interaction test
was performed. The managed configuration recipe changes to `managed-recent-cl3-1`;
existing configuration conflict rules apply to older reserved turns. No deployment
or live learning enablement occurred. M1 is not marked complete.

### CL4 — Natural outcomes and belief updates

Priority: fourth. Depends on the usable CL1–CL3 loop.

- Detect reports such as "Alex agreed, but only to review the API section" in normal
  conversation. Store the dated event without a separate outcome submission.
- Link to an earlier request only when evidence supports it. Preserve ambiguous
  outcomes as unlinked observations; ask only when a link matters to the answer.
- Retrieve semantically related belief candidates and distinguish agreement,
  contradiction, correction, changed circumstances, and unrelated statements.
  Similarity alone must not merge claims or people.
- Preserve historical evidence while changing the current working view. Do not
  equate an assistant suggestion with a sent message or infer strategy success
  merely because a favorable event was later reported.

Acceptance: a natural outcome improves later advice; paraphrased corrections update
the relevant belief; changed availability over time is not treated as an inherent
contradiction. Include ambiguous prior interactions and unrelated similar claims.

### CL5 — Meeting-note ingestion

Priority: fifth. Depends on CL4 and the demonstrated conversational learning loop.

- Start with pasted/uploaded meeting notes using existing parsing and ingestion
  capabilities, before calendar synchronization or a new connector framework.
- Let the user choose personal observations to learn versus reference material.
  Reuse extraction, provenance, inspection, correction, and forgetting from CL1–CL4.
- Keep statements attributed to their source/speaker and preserve dates; a proposal
  in meeting notes is not automatically a completed action or approved decision.

Acceptance: ingest one real meeting note, inspect extracted observations, and use
them in a later conversation without manually creating individual memory records.

### CL6 — Shared bot/workspace knowledge

Priority: last. Depends on CL5 in the agreed product order. Existing private
reference retrieval remains usable; do not expand this track ahead of CL1–CL5.

- Start with one explicitly configured shared collection per bot/deployment,
  alongside optional private knowledge. Reuse the existing knowledge service.
- Shared reference documents persist across users' conversations; personal memory
  remains subject-owned and dialogue remains conversation-specific. Never share
  personal memory by assigning all customers the same identity.
- Upload once, make the source available to the bot, and return grounded answers
  with source attribution. User conversation does not automatically become shared
  documentation. Source updates/removal must affect later retrieval.

Acceptance: two distinct customers in fresh sessions receive answers from the
same document; neither receives the other's private context; a document update
changes later answers. General sharing/permissions interfaces are later work.

### Validation and work deliberately deferred

During M1, compare history alone, history plus simple dated observations, and the
full structured context, using the same model, inputs, and reference knowledge.
Keep capture/fixtures controlled so one comparison does not teach the next one.
Use a small set of realistic multi-conversation tasks, including negative examples,
corrections, forgotten material, repeated transcripts, and expiring observations.
Judge useful recall, inappropriate capture, groundedness, changed advice, and
user effort; latency matters when it harms this experience. Automated checks prove
mechanics; inspect real answers and dogfood the usable loop for two weeks to assess
value. Start with the existing evaluation harness, not a new evaluation platform.

Pause adaptive-history expansion/enablement, cache-economics optimization, numeric
person-model refinement, scenario probability calibration, graph expansion, broad
release preparation, and unrelated integration/hardening work. Keep their completed
work and evidence. Resume only after product usefulness is demonstrated and the
priority is explicitly reconsidered, or fix a concrete blocker to the active slice.
Existing identity, acceptance, and correction/deletion behavior remain requirements
of the experience. No new production-readiness program is a prerequisite to M1.

Maintain runnable increments, relevant regression checks, and accurate feature
documentation. Use TODO.md for task status and record validation with each slice.
This plan authorizes implementation sequencing, not deployment or runtime enablement.

## Historical tracks — superseded priorities and execution records

Everything below is preserved background. Its work-order instructions, feature
deferrals, "Active" labels, and local/unstaged PLAN conventions are historical and
do not override the active CL1–CL6 plan. Completed checks describe their original
execution, not new prerequisites for conversational learning.

## Previous priority — OpenAI-compatible conversation history

Status: CH1–CH3 and consumer OCH2–OCH4 complete. CH7 release preparation and
synthetic rollback validation are implemented. Recent history is published/deployed
in `oria-live-test`; live provider continuity/erasure and capture rollback passed.
Private Telegram policy/deletion passed; conversation continuity needs a correction
and retest. CH4/CH5 observation and cache diagnostics are implemented but disabled
by default; CH6 adaptive selection is implemented, disabled by default; workload/rollout gates remain.
[ADR 0007](docs/adr/0007-conversation-history.md) and the
[history v1 contract](docs/contracts/conversation-history-v1.md) freeze the wire
shapes, pending-turn abandonment/expiry and linked-session recreation fences.
CH2 implements supplied-history Chat Completions, function tools and SSE under the
[compatibility matrix](docs/contracts/chat-completions-v1.md). CH3 now implements
managed recent replay, capabilities/finalization and migration 000005, disabled by
default. CH7 deployed gateway 0.2.2 with Oria/MCP 0.2.1 under the release gates. See [runtime operations](docs/conversation-history.md).
Historical tasks live in [TODO.md](TODO.md#previous-priority--openai-compatible-conversation-history).
The companion consumer plan is [OriaEngine PLAN section 6.3](https://github.com/bdobrica/OriaEngine/blob/main/PLAN.md#63-planned-conversation-continuity-and-openai-compatibility),
with [OCH1–OCH5 tasks](https://github.com/bdobrica/OriaEngine/blob/main/TODO.md#current-priority--conversation-history).
Sibling checkout: `../OriaEngine/`. Keep this PLAN update local under the existing
execution-record convention; commit the actionable TODO and later public contracts,
ADRs and implemented reference documentation. The earlier review-remediation plan
below remains intact and its step-specific rules continue to govern that track.

### Objective and current gap

Serve ordinary OpenAI Chat Completions clients while giving integrated consumers
reliable, optional server-managed dialogue history. The gateway exposes a
non-streaming Responses subset plus CH2 supplied-history
Chat Completions (tools and SSE). Responses builds model messages from the current
request and retrieved context, persists exchanges, but does not load prior dialogue.
Ordinary Chat Completions preserves caller messages and shares bounded retrieval;
it does not archive full supplied transcripts or generated drafts. CH3 adds opt-in
managed capture/replay in separate canonical turn storage. Existing
`MessageRepository.ListBySession` selects the oldest messages, not a recent window.
Existing assistant records are generated drafts, not proof of consumer acceptance
or delivery. Stable session identity and semantic-memory recall do not close this gap.

### Ownership and compatibility

SecondContext owns transcript persistence, prompt assembly, context selection and
OpenAI Decisions calls. Oria owns consent, canonical private records, current chart
calculations, input preparation, accepted reply validation and channel delivery.
Use versioned HTTP only; no direct cross-repository database or internal-type access.
Keep memory, linked projections and reference knowledge as independent context lanes.
Classifier output may select authorized context but cannot expand account, tool or
service authority or replace deterministic salience under ADR 0002.

CH1 created a new ADR and additive public history contract;
do not rewrite accepted ADRs. Explicitly extend ADR 0001/0006's service allowlist for
Chat Completions and the two new context routes, preserving subject resolution,
advisory locks, namespace restrictions and deletion fences. Review ADR 0004's context
controls and update architecture docs without changing their existing meanings.

### Accepted API surface

CH1 froze these names in the owning contract. It also specifies payload-free
`abandon` through finalize, a 24-hour pending deadline, terminal session fences,
and a text-only/non-streaming managed profile. Ordinary supplied-history Chat
Completions still includes tools and SSE; v1 managed tool/stream support is deferred.
The contract/schema take precedence over this execution summary:

| Surface | Accepted behavior |
| --- | --- |
| `POST /v1/chat/completions` | Standard request/response and SSE streaming; client supplies conversation in `messages`. |
| `POST /v1/responses` | Preserve existing accepted requests and result shape; share the internal pipeline where semantics match. |
| `GET /v1/models` | Preserve model discovery; document supported model/feature combinations. |
| `POST /v1/context/capabilities` | Authenticated, subject-scoped `conversation-history-1` discovery: modes, acceptance support, message types and limits. |
| `POST /v1/context/turns/finalize` | Authenticated acceptance of a generated draft/replacement or an application-origin prepared local exchange. |

Optional managed-history headers on generation endpoints:

| Header | Meaning |
| --- | --- |
| `X-SecondContext-Session` | Stable external session ID belonging to the authenticated subject. |
| `X-SecondContext-History` | `off`, `recent`, or `adaptive`; omitted means no automatic stored-history replay. |
| `X-SecondContext-Turn` | Stable caller turn ID with immutable request binding and retry semantics. |
| `X-SecondContext-Acceptance` | `generated` or `explicit`; ordinary clients require no finalization. |

These do not replace `X-SecondContext-Subject`. Existing session metadata continues
to work; conflicting selectors fail. Managed replay requires authenticated storage,
an explicit session and a defined turn identity. Without a session, supplied messages
define the conversation; never merge separate chats because they share a user/key.
Client-supplied history remains the request's dialogue source and is not silently
trimmed by Decisions or merged with stored history. Reject ambiguous combinations
initially. CH1 specifies header parsing, allowed combinations and bounded errors.
History `off` does not disable semantic memory or knowledge.

Standard clients use normal base URL, credentials and model configuration, without
custom IDs or lifecycle requests. Preserve roles, supported content blocks, tool
call IDs/results, finish reasons, usage and errors; implement SSE deltas, terminal
events, usage options, cancellation and incomplete-stream handling. Publish an exact
support matrix for common generation controls, tools, structured output and modalities.
Do not silently drop behavior-affecting unsupported fields or claim complete OpenAI
API parity. Preserve existing Responses compatibility separately.

Finalize binds contract version, subject, session ID, turn ID, response ID and
accepted assistant content; its exact acknowledgement includes status and durable
sequence. An application-origin variant supplies prepared user/assistant text without
a response ID. CH1 defines closed schemas, source references and RFC 8785 hash canonicalization.
Same ID/content retries return the same result; conflicting content returns 409.
Foreign ownership fails without disclosure. Version history diagnostics separately
and expose them through optional headers rather than altering standard response bodies.
No general transcript-list/edit API or session CRUD is required for this release.

### Turn lifecycle and baseline replay

Add durable ordering, request binding, completion/acceptance state and a generation
snapshot boundary, using a migration where needed. Scope queries by owner and session;
load latest eligible turns then return them chronologically. Keep supplied inbound
messages separate from assembled prompts so replay never re-inserts prior messages.
Test equal timestamps, concurrent attempts and multiple replicas explicitly.

In generated mode, completed generation is eligible for replay, without a delivery
claim. In explicit mode, only finalized application-accepted text is replayable;
rejected drafts and failed/incomplete streams are excluded. Existing retained drafts
are not automatically promoted during migration. Implement the contracted pending-turn barriers,
abandonment/recovery and terminal failure behavior before enabling managed sessions.
Replay a durably saved result on matching retries where possible; crashes around an
upstream call can still cause duplicate provider execution. Do not promise exactly once.

Oria commits validated reply plus encrypted finalization work together, finalizes
post-commit, then delivers via Telegram. Application acceptance and delivery are
separate. Later generation waits for consumer pending-work resolution. Local turns
are narrowly prepared by Oria; raw birth/profile/onboarding/secret data stays local.
Do not infer remote finalization from a temporary local save or infer delivery from
a successful HTTP response. Consumer cancellation never disables required erasure.

Start with a configurable recent window (evaluate eight complete exchanges), bounded
also by the whole prompt budget, including instructions, current input, retrieved
sections, supported tool blocks and output reservation. Keep coherent turn/tool groups;
define oversize rejection/omission behavior instead of breaking message structure.
Historical claims remain dialogue, not fresh chart facts or trusted instructions.

### Decisions, caching and older history

Implement a bounded OpenAI Decisions client separately from generation and embeddings;
use `POST /v1/decisions` with the documented supported model (`gpt-6-luna` at planning
time), configured credentials/base URL and a versioned question recipe. Verify access
and current API details before live use. Start in observation mode: calculate routing
but keep the recent-history baseline. Ask separately whether current input depends
on recent dialogue and whether it refers to older discussion outside that window.
Calibrate thresholds using labeled conversations; weigh missed context more heavily
than unnecessary history. Refusal, ambiguity, timeout, invalid answers or upstream
failure preserve recent history. Never apply this fallback after an authorization,
deletion-fence or canonical-history validation failure.

The [Decisions guide](https://developers.openai.com/api/docs/guides/decisions) reviewed
2026-11-09 documents a matching shared-input prefix of at least 1,024 tokens; question
content is outside that prefix. Use stable shared input, newest message last, explicit
breakpoints where helpful and bounded append-growing history segments before rotation.
Do not slide the first message on every turn and assume prefix reuse. Partition opaque
cache keys by subject/session/context generation; correction/deletion changes that
generation and request content. Rehydrate/revalidate canonical eligibility before
every classifier/generator call. Never replay deleted material solely to obtain a hit.
Provider cache keys are not authority or deletion acknowledgements; provider retention
remains separately disclosed. Do not assume cache sharing between Decisions and chat.

Measure cached/read/write/total input usage without double counting, latency, fallback
rate and total classifier-plus-generation cost. No padding merely to reach the cache
threshold; compare cold/warm short and long conversations. Keep raw transcripts,
credentials and subject IDs out of metric labels and routine logs.

After baseline validation, adaptive mode may omit recent dialogue for well-supported
standalone requests. It can also trigger bounded older-turn retrieval within the same
session, with canonical hydration and chronological excerpts. Older-history absence
is not permission to invent references. Cross-session replay, automatic summaries,
new transcript embeddings and transcript-to-memory extraction are deferred. Any later
derived store or copied history first needs source dependency tracking and erasure.

### Lifecycle, dependencies and release gates

Before replay or classification, respect active subject fences and linked projection
revisions, including pending erasure even when a request supplies no new knowledge
refs. Preserve entire-linked-session cleanup under ADR 0006; extend coverage to new
turn rows, receipts and any derived selection state. Corrections append dialogue;
explicit forgetting/canonical source invalidation removes affected eligible history.
Full purge fences late finalize calls and cascades new subject content. Specify minimal
payload-free retry receipts and their retention; no transcript shadow store survives.

Order: CH1 contract agreement with OCH1 → CH2 compatibility and CH3 lifecycle/recent
history → OCH2/OCH3 adapter and acceptance → OCH4 cross-repository baseline verification.
CH4/CH5 observation/cache and CH6 opt-in adaptive selection are implemented.
CH6 paired evaluation preserves synthetic continuity, but aggregate cost/latency
regressed; keep production recent pending workload evidence and consumer rollout. CH7/OCH5 release gates apply to the baseline and again to adaptive enablement. CH2 and CH3 may be separate commits but both precede the first
history-enabled release. First release includes recent history and optional observation;
active adaptive routing and older recall are later gates, not prerequisites to continuity.

Acceptance uses unchanged OpenAI SDK clients, synthetic request captures and real
isolated Postgres integration checks: streaming/tool sequences, supplied transcripts,
multiple chats per subject, follow-ups, replacements, local turns, retries, restart,
ordering, budgets, purge, pending selective erasure and stale source corrections.
Run focused tests, `make verify`, relevant race tests and Oria consumer/replay gates.
Evaluate model quality and optional paid provider/cache checks separately from CI.
Include the Oria private-reading handoff (horoscope, compatibility/private date,
plain-English follow-up, then a new topic) in CH4 evaluation. Oria supplies only
eligible prepared input and current facts; the classifier cannot reconstruct an
excluded private exchange. Evaluate response relevance separately from replay input.
See the companion [handoff evidence](https://github.com/bdobrica/OriaEngine/blob/main/docs/evidence/reading-focus-handoff.md).
Record durable evidence in `docs/evidence/`; update public docs/config/TODO with each
verified slice and keep this local PLAN outside implementation commits.

Publish compatible versioned images pinned by digest; verify capability before consumer
enablement and review actual disclosure changes in Oria. Coordinate ingress/workers
and storage migrations; health alone proves neither generation nor erasure. Restrict
live operations to `oria-live-test`, preserve unrelated projects, and leave the separate
external `KNOWLEDGE_ENABLED` integration disabled. Roll back adaptive to recent without
losing accepted history; disabling history/generation must preserve finalize recovery
and erasure. Verify rollback with the new schema before deployment.

### CH2 execution record — 2026-11-09

Implemented supplied-history Chat Completions, scoped service authorization, validated
raw wire forwarding, tools/structured output, bounded SSE and cancellation. No managed
history advertisement or runtime/deployment change in Oria. No Go dependency added.
Verification and limitations: [CH2 evidence](docs/evidence/chat-completions-ch2.md).
Status: Completed. Commit: `69d491a5b4d87606a9e3235436179f3ac786fe37`.
`make verify` (including OpenAI SDK 3.48.0 and real Postgres/Qdrant tests) and focused
API/LLM race checks passed. Keep this record unstaged.

### CH3 execution record — 2026-11-09

Implemented durable managed recent history, exact result/request binding, generated
and explicit acceptance, atomic local exchanges, recovery/expiry/abandonment and
whole-linked-session fences. Default deployment capture remains disabled. Published
operations and verification: [CH3 evidence](docs/evidence/conversation-history-ch3.md).
Root `make verify` (real Postgres/Qdrant, unchanged OpenAI SDK 3.48.0), managed
integration race checks and Oria's 32 schema tests passed. Schema copies remain
identical; no consumer runtime/deployment changes were made. No dependencies added.
Next: OCH2/OCH3 consumer capability/acceptance/disclosure work, then OCH4 baseline
continuity checks; Decisions observation follows that working baseline.
Status: Completed. Commit: `f0dd2682e215fb72520224509eee04cbf14ba805`. Keep this record unstaged.

### CH7 execution record — 2026-11-10

Prepared gateway 0.2.2 for the recent-only release with Oria/MCP 0.2.1. Updated
operations, configuration guidance and coordinated rollback sequence; expanded the
packaged-image gate for readiness, accepted receipt recovery, capture disablement
and purge on migration 5. Oria adds independent deployment controls, a private
cleanup-journaled live smoke command and real-gateway rollback coverage.
See [CH7 evidence](docs/evidence/conversation-history-ch7.md) for exact verification.
Gateway 0.2.2 and Oria/MCP 0.2.1 were published, verified by digest/source labels
and deployed only to `oria-live-test`. Owning migrations, real-provider continuity,
accepted replacement/local replay, selective/full erasure and a capture-off rollback
drill passed. Fresh disclosure acceptance is required. The participating user's
private Telegram policy/deletion passed, but continuity failed. The companion
Oria 0.2.2 correction is deployed; live synthetic reading/follow-up/date probes passed.
Private Telegram retest remains. CH4–CH6 remain separate.
Status: Recent baseline released; private Telegram continuity correction/retest pending. Keep this local
execution record unstaged under the existing convention.

## Earlier review remediation plan

Created: 2026-07-20
Plan status: Historical; priority superseded by CL1–CL6 above
Source of work: `CODE_REVIEW.md`

## Objective

Resolve the code-review findings in dependency order while preserving existing user work, adding regression coverage, keeping public documentation accurate, and creating one narrowly scoped implementation commit per successful step.

`PLAN.md` and `CODE_REVIEW.md` are internal execution records. Their updates must remain outside implementation commits.

## Current worktree baseline

The user-owned observability/configuration work present during the review was committed before remediation began:

- commit: `a3f0509` (`feat(observability): add API logs and metrics`);
- internal records remaining outside the commit: `PLAN.md` and `CODE_REVIEW.md`.

Several planned steps touch files changed by that baseline commit. Inspect the live code and diff first and preserve its observability behavior while implementing remediation.

## Operating rules

These were the rules for the historical remediation track. They do not govern
CL1–CL6; in particular, PLAN.md is now a shared, committed document.

1. Start only when all dependencies listed for the step are complete.
2. Re-read the relevant finding in `CODE_REVIEW.md` and inspect the current code before editing; line numbers may have moved.
3. Set the step to `In progress` in this file before code changes. This status-only edit remains unstaged.
4. Keep each step narrowly scoped. Do not combine unrelated cleanup or formatting.
5. Preserve user changes already present in the worktree. If a planned file has overlapping edits, understand and retain their intent.
6. Implement tests with the change. A step is not successful until every acceptance check passes.
7. After implementation and verification, update all affected public documentation in the same implementation commit. At minimum, inspect:
   - `README.md`;
   - `TODO.md`;
   - `.env.example` for configuration changes;
   - relevant files under `docs/`;
   - `deploy/README.md`, `docker-compose.yml`, and `Makefile` for deployment or workflow changes.
8. Create one implementation commit for the successful step. Stage files explicitly; never use `git add .` or `git add -A`.
9. Before committing, inspect both the staged content and file list:

   ```bash
   git diff --cached
   git diff --cached --name-only
   git diff --cached --name-only | grep -E '(^|/)(PLAN|CODE_REVIEW)\.md$' && exit 1 || true
   ```

10. `PLAN.md` and `CODE_REVIEW.md` remain modified or untracked in the local worktree. Never stage, commit, amend, stash, or otherwise publish their content as part of this plan.
11. Commit only after the staged diff contains the intended code, tests, and public documentation and all acceptance checks pass.
12. After committing, obtain the immutable hash with `git rev-parse HEAD`. Then, without amending the commit:
    - change the plan step from `In progress` to `Completed`;
    - record completion date, commit hash, tests run, and a short implementation note in its execution record;
    - mark each corresponding review finding `Resolved` in `CODE_REVIEW.md`, without deleting its history;
    - if implementation reveals a new issue, add it to `CODE_REVIEW.md` and schedule it here or in `TODO.md`.
13. If checks fail, do not commit. Keep the step `In progress` while actively diagnosing it. Mark it `Blocked` only when it cannot continue, and record the concrete reason and diagnostic evidence.
14. A step is `Completed` only when acceptance checks pass, public documentation is accurate, an implementation commit exists, and the post-commit internal record updates are present locally.

### Commit convention

Use a concise conventional subject where practical:

```text
fix(scope): outcome
test(scope): outcome
feat(scope): outcome
docs(scope): outcome
ci(scope): outcome
```

Each implementation commit should contain code, tests, and affected public documentation. Documentation-only follow-up commits should be exceptional.

### Step status values

- `Pending` — not started
- `In progress` — active implementation
- `Blocked` — cannot continue; explanation required
- `Completed` — acceptance checks passed, public docs updated, and implementation commit created

### Standard verification

Use an isolated writable Go cache if the environment's default cache is read-only:

```bash
GOCACHE=/tmp/secondcontext-go-cache go test ./...
GOCACHE=/tmp/secondcontext-go-cache go vet ./...
```

Run `gofmt` on every changed Go file before tests. Use narrower package tests during development, then run the step's full acceptance suite.

## Dependency order

```text
Step 1 (identity authority)
  ├── Step 2 (token/config fail-closed)
  │     └── Step 3 (trusted proxies)
  └── Step 4 (resource bounds)

Step 5 (outcome consistency) depends on Steps 1 and 4
Step 6 (mandatory integration lane) depends on Steps 1-5
Step 7 (final verification) depends on Steps 1-6
```

Step 2 combines SC-002 and SC-003 because both change authentication configuration parsing in `internal/config/config.go` and must establish one fail-closed startup contract. The acceptance checks still track both findings independently.

## Implementation steps

### Step 1 — Establish one authoritative authenticated request identity

- Status: `Completed`
- Findings: SC-001
- Dependencies: None
- Suggested commit: `fix(auth): enforce tenant identity before context retrieval`

Implementation:

1. Add a request-identity or resolved-metadata value that is computed once after authentication and before handlers perform data access.
2. Define the conflict policy for authenticated requests. Prefer rejecting conflicting `user` and `metadata.user_external_id` selectors with a stable 4xx error; alternatively ignore them consistently if OpenAI client compatibility requires it. Document the selected behavior.
3. Refactor `/v1/responses` so context construction and persistence receive the same resolved identity. Do not let `buildBaseContextPacket` independently prioritize raw selectors.
4. Audit every handler that accepts `user`, `user_external_id`, session IDs, message IDs, memory IDs, person IDs, or topic IDs and route it through the same authority rule.
5. Add a database-backed regression test that:
   - seeds tenant B with a uniquely identifiable memory/person/belief;
   - authenticates as tenant A;
   - submits both `metadata.user_external_id=tenant-b` and `user=tenant-b` variants;
   - proves tenant B data is absent from the retrieval request, upstream prompt, response text fixture, and `metadata.context_packet`;
   - proves the request does not create or alter tenant B state.
6. Add unit tests for identity conflict behavior that do not require Postgres.
7. Update README authentication/isolation behavior and relevant docs.

Acceptance checks:

```bash
GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api -run 'TestAuthenticated|Test.*Tenant|Test.*Isolation|Test.*RequestUser'
GOCACHE=/tmp/secondcontext-go-cache go test ./...
GOCACHE=/tmp/secondcontext-go-cache go vet ./...
```

Also run the new database-backed test with the integration environment from Step 6 if one already exists locally. Until then, record whether it ran or skipped.

Execution record:

- Completion date: 2026-07-20
- Commit: `44f16f07b1c11312c106cb89c08016ff70dbfb63`
- Tests:
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api -run 'TestAuthenticated|Test.*Tenant|Test.*Isolation|Test.*RequestUser'` — passed; database-backed cases skipped because `POSTGRES_DSN` is unset.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./...` — passed; database-backed cases skipped because `POSTGRES_DSN` is unset.
  - `GOCACHE=/tmp/secondcontext-go-cache go vet ./...` — passed.
  - `git diff --check` and staged internal-file exclusion check — passed.
- Implementation note: Added one authenticated user-selector resolver with a documented HTTP 400 `identity_conflict` policy, applied it across response, memory, outcome, and debug endpoints, and made response retrieval and persistence consume the same resolved metadata. Added unit coverage for all selector entry points plus a Postgres regression that seeds and snapshots tenant B memory/person/topic/belief state; the integration test is ready but did not execute locally because `POSTGRES_DSN` is unset.

### Step 2 — Make authentication configuration fail closed

- Status: `Completed`
- Findings: SC-002, SC-003
- Dependencies: Step 1
- Suggested commit: `fix(config): reject ambiguous authentication settings`

Implementation:

1. Replace `getEnvBool` with error-returning parsing and update `Load` to propagate errors for every boolean setting.
2. Reject non-empty invalid values for `AUTH_ENABLED`, `POSTGRES_ENABLED`, `HTTP_METRICS_ENABLED`, and any future boolean option.
3. Require a non-empty subject for every bearer-token entry when authentication is enabled. Remove acceptance of bare tokens unless an explicit compatibility mode is designed, documented, and proven safe.
4. Add a middleware invariant: a successful authentication result must contain a non-empty normalized subject.
5. Validate duplicate subjects and duplicate token values. Reject ambiguous mappings rather than relying on first-match order.
6. Avoid logging secret token values in configuration errors.
7. Add table-driven configuration tests covering unset values, all accepted `strconv.ParseBool` forms chosen for support, invalid values, blank subjects, bare tokens, duplicate subjects, duplicate token values, and valid multi-user configuration.
8. Add an API test proving no accepted token can produce an empty authenticated subject.
9. Update `.env.example`, README, and deployment documentation with the exact token grammar and fail-fast behavior.

Acceptance checks:

```bash
GOCACHE=/tmp/secondcontext-go-cache go test ./internal/config ./internal/api -run 'Test.*(Config|Auth|Token|Bool|Subject)'
GOCACHE=/tmp/secondcontext-go-cache go test ./...
GOCACHE=/tmp/secondcontext-go-cache go vet ./...
```

Execution record:

- Completion date: 2026-07-20
- Commit: `4cbb0c3c28cdad3a2b19dc29fa69c95b1902b837`
- Tests:
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/config ./internal/api -run 'Test.*(Config|Auth|Token|Bool|Subject)'` — passed.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./...` — passed; the first sandboxed run was blocked when an existing `httptest` case could not open a loopback socket, then passed with loopback access enabled.
  - `GOCACHE=/tmp/secondcontext-go-cache go vet ./...` — passed.
  - `git diff --check`, staged diff review, and staged internal-file exclusion check — passed.
- Implementation note: Replaced permissive boolean fallbacks with startup errors for every boolean setting; made bearer-token parsing require unique, non-empty `subject=token` entries without disclosing secrets in errors; and added a defensive middleware invariant that refuses subjectless principals. Added table-driven configuration coverage, an API regression, and exact grammar/fail-fast guidance in the environment template, README, and deployment documentation.

### Step 3 — Introduce an explicit trusted-proxy model

- Status: `Completed`
- Findings: SC-004
- Dependencies: Step 2
- Suggested commit: `fix(http): trust forwarded addresses only from configured proxies`

Implementation:

1. Add configuration for trusted proxy CIDRs or an explicit forwarding-header mode. Default to trusting no proxy.
2. Replace unconditional `middleware.RealIP` with middleware that:
   - captures the immediate socket peer;
   - honors configured forwarding headers only when that peer belongs to a trusted proxy;
   - selects the client address from a well-defined hop direction;
   - rejects or safely ignores malformed addresses.
3. Make rate limiting and request logging consume the same resolved client address.
4. Decide and document behavior for multiple proxies, IPv4-mapped IPv6 addresses, and direct Docker port publishing.
5. Add table-driven tests for direct spoofed headers, one trusted proxy, multiple forwarding hops, untrusted proxies, malformed values, IPv4, and IPv6.
6. Add a regression test proving a direct unauthenticated client cannot rotate `X-Forwarded-For` to bypass its limit.
7. Update `.env.example`, README, Docker/deployment docs, and observability documentation.

Acceptance checks:

```bash
GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api -run 'Test.*(RealIP|ClientIP|Forwarded|Proxy|RateLimit|RequestLogging)'
GOCACHE=/tmp/secondcontext-go-cache go test ./...
GOCACHE=/tmp/secondcontext-go-cache go vet ./...
```

Execution record:

- Completion date: 2026-07-20
- Commit: `62fc90b8ee81a10885cb6796751d4830c9d7f6b5`
- Tests:
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/config ./internal/api -run 'Test.*(Config|RealIP|ClientIP|Forwarded|Proxy|RateLimit|RequestLogging)'` — passed.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api -run 'Test.*(RealIP|ClientIP|Forwarded|Proxy|RateLimit|RequestLogging)'` — passed.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./...` — passed; the first sandboxed run was blocked when an existing `httptest` case could not open a loopback socket, then passed with loopback access enabled.
  - `GOCACHE=/tmp/secondcontext-go-cache go vet ./...` — passed.
  - `git diff --check`, staged diff review, and staged internal-file exclusion check — passed.
- Implementation note: Replaced unconditional forwarding-header trust with a CIDR-configured client-IP resolver that defaults to the socket peer, accepts only `X-Forwarded-For` from an immediate trusted proxy, evaluates multi-proxy chains right to left, falls back safely on malformed input, and normalizes IPv4-mapped IPv6 addresses. Rate limiting and structured request logs now consume the same resolved address. Added configuration, chain, logging, and header-rotation regressions, plus environment, README, Compose, and deployment guidance.

### Step 4 — Bound and strictly validate external input and responses

- Status: `Completed`
- Findings: SC-005
- Dependencies: Step 1
- Suggested commit: `fix(api): enforce request and result limits`

Implementation:

1. Define named, documented limits for:
   - JSON request body bytes;
   - response input text and metadata where needed;
   - list/search page sizes;
   - Qdrant candidate expansion;
   - OpenAI and Qdrant response body bytes.
2. Implement one shared JSON decoder that applies `http.MaxBytesReader`, performs exactly one value decode, verifies EOF, and produces stable errors for malformed, trailing, or oversized bodies.
3. Decide unknown-field behavior per endpoint. Preserve required OpenAI compatibility while using strict decoding on internal endpoints where safe.
4. Enforce maximum list/search limits at the API and service boundaries. Use overflow-safe arithmetic for candidate expansion.
5. Replace unbounded `io.ReadAll` calls with limited reads that distinguish at-limit from over-limit responses.
6. Ensure error bodies do not echo oversized or sensitive upstream payloads.
7. Add boundary tests for empty bodies, exact maximums, one byte/item over, trailing JSON, negative/zero/excessive limits, multiplication overflow, and oversized OpenAI/Qdrant responses.
8. Update README API constraints and any demo/evaluation clients that need explicit limits.

Acceptance checks:

```bash
GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api ./internal/llm ./internal/qdrant ./internal/retrieval -run 'Test.*(Body|JSON|Limit|Oversize|Response)'
GOCACHE=/tmp/secondcontext-go-cache go test ./...
GOCACHE=/tmp/secondcontext-go-cache go vet ./...
```

Execution record:

- Completion date: 2026-07-20
- Commit: `bfd793e74b41092aa77b01f8b3304bea8efd8d92`
- Tests:
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api ./internal/llm ./internal/qdrant ./internal/retrieval -run 'Test.*(Body|JSON|Limit|Oversize|Response)'` — passed.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./...` — passed.
  - `GOCACHE=/tmp/secondcontext-go-cache go vet ./...` — passed.
  - `git diff --check`, staged diff review, and staged internal-file exclusion check — passed.
- Implementation note: Added one 1 MiB JSON request decoder with stable empty, malformed, trailing-value, and oversized errors. Internal mutation endpoints reject unknown fields while `/v1/responses` remains forward-compatible; response input and metadata now have separate 256 KiB and 64 KiB ceilings. List, search, candidate, and prefetch limits are enforced at API, service, retrieval, and Qdrant boundaries with overflow-safe expansion. OpenAI and Qdrant responses use typed bounded reads (8 MiB and 16 MiB) and no longer expose upstream error bodies. Boundary, propagation, and non-disclosure regressions were added, and the public limits are documented in `README.md`.

### Step 5 — Make outcome updates idempotent and recoverable

- Status: `Completed`
- Findings: SC-006
- Dependencies: Steps 1 and 4
- Suggested commit: `fix(outcomes): make feedback updates recoverable`

Implementation:

1. Document the intended source of truth and state machine for outcome processing before changing code.
2. Introduce a caller-supplied or server-derived stable idempotency key. Persist it with a uniqueness constraint so retries converge on one logical outcome.
3. Put the canonical outcome row and all related Postgres writes that must be atomic into a transaction. Refactor repositories to accept a transaction-capable interface instead of hard-coding `*pgxpool.Pool`.
4. Treat Qdrant indexing and derived person/belief updates as explicit work with durable status (`pending`, `completed`, `failed`) or an equivalent outbox/reconciliation design.
5. Do not discard model/belief update errors. Return them when rollback is possible; otherwise durably record failure, emit structured diagnostics/metrics, and provide a retry/reconciliation path.
6. Prevent duplicate graph edges and memories on retry using stable identifiers or uniqueness constraints.
7. Add failure-injection tests after analysis, memory creation, Qdrant upsert, each graph-edge write, outcome insertion, person-model update, and belief update.
8. Prove that retrying every injected failure reaches one complete outcome without orphaned or duplicated canonical state.
9. Add migration up/down coverage and update architecture, backup/restore, observability, README, and TODO documentation.

Acceptance checks:

```bash
GOCACHE=/tmp/secondcontext-go-cache go test ./internal/outcomes ./internal/memory ./internal/db
GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api -run 'Test.*Outcome'
GOCACHE=/tmp/secondcontext-go-cache go test ./...
GOCACHE=/tmp/secondcontext-go-cache go vet ./...
```

The database-backed failure and retry tests must execute, not skip. Record the integration command and dependency versions in the execution record.

Execution record:

- Completion date: 2026-07-20
- Commit: `4dab7659d72700a28193234ebda4bd3178de7256`
- Tests:
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/outcomes ./internal/memory ./internal/db` — passed.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api -run 'Test.*Outcome'` — passed.
  - `OUTCOME_INTEGRATION=1 POSTGRES_DSN=<local-test-dsn> GOCACHE=/tmp/secondcontext-go-cache go test ./internal/outcomes -run TestOutcomeFailureRetriesConverge -count=1 -v` — passed without skips for all eight injected stages.
  - `POSTGRES_DSN=<local-test-dsn> GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api -run 'Test.*Outcome' -count=1 -v` — passed without skips.
  - `POSTGRES_DSN=<local-test-dsn> POSTGRES_ENABLED=true GOCACHE=/tmp/secondcontext-go-cache go run ./cmd/migrate down 1`, followed by `go run ./cmd/migrate up` and `go run ./cmd/migrate version` — passed; final state `version=2 dirty=false`.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./...` — passed.
  - `GOCACHE=/tmp/secondcontext-go-cache go vet ./...` — passed.
  - `git diff --check`, staged diff review, and staged internal-file exclusion check — passed.
  - Integration dependencies: Go 1.24.10, PostgreSQL 16.13; Qdrant behavior used the deterministic in-process HTTP test double.
- Implementation note: Made Postgres the canonical outcome source before cross-system work. Tenant-scoped caller or derived idempotency keys, request hashes, stable outcome-memory identities, and outcome-scoped graph uniqueness make retries converge. Migration 000002 adds durable pending/completed/failed state and error details for Qdrant, person-model, belief, and overall outcome processing. Repositories support transaction-capable database handles; derived errors are returned and recorded; duplicate model/belief evidence is ignored. Failure injection after analysis, canonical insertion, memory creation, Qdrant upsert, person and belief updates, and each graph edge proves retry convergence to one outcome, one memory, unique edges, and one evidence contribution. README, TODO, architecture, deployment, migration, backup/restore, observability, and recovery documentation were updated.

### Step 6 — Make security integration tests mandatory and visible

- Status: `Completed`
- Findings: SC-007
- Dependencies: Steps 1-5
- Suggested commit: `ci(test): require tenant isolation integration suite`

Implementation:

1. Separate fast unit tests from dependency-backed integration tests using a clear convention such as a build tag plus Make targets.
2. Keep developer-friendly skips only in the fast/unit path. In the integration target, missing or unreachable dependencies must fail immediately.
3. Add a deterministic Compose or CI service setup for Postgres and Qdrant, run migrations, execute all tenant-isolation and cross-store tests, and clean up.
4. Pin service versions and add readiness checks rather than fixed sleeps.
5. Add a CI workflow that runs:
   - formatting verification;
   - `go vet`;
   - unit tests;
   - mandatory integration tests;
   - optionally `go test -race` for packages that exercise concurrent middleware and metrics.
6. Ensure CI output makes executed and skipped test classes explicit.
7. Add Make targets such as `test-unit`, `test-integration`, and `verify`, with `verify` matching required CI checks.
8. Update README, `deploy/README.md`, and TODO with prerequisites and exact local commands.

Acceptance checks:

```bash
make test-unit
make test-integration
make verify
git diff --check
```

Confirm from verbose output that the SC-001 cross-tenant regression and all existing isolation tests ran rather than skipped.

Execution record:

- Completion date: 2026-07-20
- Commit: `e2395a8279dad62bc3e844eb13e072ece03edaaa`
- Tests:
  - `GOCACHE=/tmp/secondcontext-go-cache make test-unit` — passed; output explicitly identified the `-short` lane and its dependency-backed exclusions.
  - `GOCACHE=/tmp/secondcontext-go-cache make test-integration` — passed with pinned Postgres 16.13 and Qdrant 1.15.5 Compose services, readiness checks, migrations, verbose output, and zero skipped tests.
  - `GOCACHE=/tmp/secondcontext-go-cache make verify` — passed formatting verification, `go vet ./...`, unit tests, migrations, and the mandatory integration suite.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api ./internal/qdrant` — passed after correcting integration fixtures and preserving typed Qdrant missing-collection classification.
  - `git diff --check`, staged diff review, and staged internal-file exclusion check — passed.
  - Integration runtime: Go 1.24.10, PostgreSQL 16.13, Qdrant 1.15.5.
- Implementation note: Added explicit `test-unit`, `test-integration`, and CI-equivalent `verify` targets. The integration runner provisions isolated pinned services when no DSN is supplied, waits for health rather than sleeping, applies migrations, runs all integration-capable packages verbosely, fails on any skip or dependency error, and cleans up services and volumes. GitHub Actions supplies the same pinned healthy services and makes `make verify` required. The first mandatory run exposed stale derived-update fixtures and a lost Qdrant missing-collection classification; both were corrected, after which the SC-001 cross-tenant response regression and all existing isolation tests ran visibly without skips. README, deployment guidance, and TODO now document the lanes and prerequisites.

### Step 7 — Final remediation verification and documentation audit

- Status: `Completed`
- Findings: All
- Dependencies: Steps 1-6
- Suggested commit: `docs(hardening): align operational guidance` only if public documentation changes are still required

Implementation:

1. Re-read every finding and inspect the final code paths rather than relying only on prior execution records.
2. Run the complete repository verification, including race detection for concurrency-sensitive packages if it is not already part of `make verify`.
3. Review all public docs for stale security, API-limit, proxy, retry, and testing claims.
4. Verify migrations apply and roll back on a clean database.
5. Verify the Docker Compose development flow and health/metrics behavior.
6. Confirm no secrets, local DSNs, generated reports, `PLAN.md`, or `CODE_REVIEW.md` are staged.
7. If no public file needs a change, do not create an empty or record-only commit. Record the verification locally and mark the step completed with `Commit: N/A (verification only)`.
8. If a real public-doc correction is needed, commit only that correction after the standard staged-file exclusion checks.

Acceptance checks:

```bash
make verify
GOCACHE=/tmp/secondcontext-go-cache go test -race ./internal/api ./internal/llm ./internal/retrieval ./internal/scoring
git diff --check
git status --short
git diff --cached --name-only
git diff --cached --name-only | grep -E '(^|/)(PLAN|CODE_REVIEW)\.md$' && exit 1 || true
```

Execution record:

- Completion date: 2026-07-20
- Commit: `N/A (verification only)`
- Tests:
  - `GOCACHE=/tmp/secondcontext-go-cache make verify` — passed formatting verification, `go vet`, the fast `-short` suite, migrations, and the mandatory verbose integration suite with zero skipped tests.
  - `GOCACHE=/tmp/secondcontext-go-cache go test -race ./internal/api ./internal/llm ./internal/retrieval ./internal/scoring` — passed; the initial sandboxed run was blocked when `httptest` could not open an IPv6 loopback listener, then passed with loopback access enabled.
  - On a fresh pinned PostgreSQL 16.13 container, `go run ./cmd/migrate up`, two successive `go run ./cmd/migrate down 1` commands, `go run ./cmd/migrate up`, and `go run ./cmd/migrate version` — passed; final state `version=2 dirty=false`.
  - `docker compose config --quiet` and `docker compose up -d --build --wait` — configuration and image build passed. A host-side `modelsays-postgres` container already owned port 5432, so the endpoint smoke test used the same Compose service definitions on the project network without publishing the Postgres/Qdrant host ports. The API started successfully, `/healthz` returned HTTP 200 with `postgres=ok`, and `/metrics` returned the documented Prometheus metrics. All temporary containers and disposable integration volumes were removed.
  - `git diff --check`, `git status --short`, staged-file review, internal-file exclusion check, and ignored-artifact review — passed. The staging area is empty; only local `PLAN.md` and `CODE_REVIEW.md` records are changed or untracked. Existing ignored `.env` and `.artifacts/` content remains unstaged.
- Implementation note: Re-read all findings and inspected the final identity, authentication configuration, trusted-proxy, bounded-input/upstream-response, outcome-recovery, migration, and integration-test paths against their public documentation. No stale or inaccurate public claim required a tracked-file change, so no empty or record-only commit was created. Final verification visibly executed the cross-tenant response regression and every mandatory integration class without skips.

## Completion checklist

The remediation is complete only when:

- all seven steps are `Completed`;
- SC-001 through SC-007 are marked `Resolved` without removing their original descriptions;
- each implementation step has a commit hash and recorded passing checks;
- mandatory integration output proves tenant-isolation tests executed;
- public documentation matches actual behavior;
- `git diff --cached --name-only` excludes `PLAN.md` and `CODE_REVIEW.md`;
- the internal records remain local and contain the final hashes and status history.

### CH4/CH5 implementation — 2026-11-11

Observation-only Decisions HTTP integration, canonical bounded cache segments,
scoped HMAC keys, typed fallbacks, numeric diagnostics and synthetic evaluator
are implemented. Runtime generation remains recent; no public contract/schema
change, model replacement or production enablement. See
[durable evidence](docs/evidence/conversation-history-ch45.md). Live synthetic
checks found cache reuse for long evidence and uncertainty in three scenarios;
those are CH6 calibration inputs, not grounds to enable adaptive routing.


## CH6 — implemented 2026-11-11

`standalone-1` uses the conservative 0.05 dual-dependency cutoff; unknown and older
references preserve recent. Adaptive capability has a separate default-off provider
switch. Selection commits once before generation in existing selected-turn storage;
retries/restarts reuse it, while source invalidation/deletion abort instead of falling
back. No migration, older retrieval or cross-session derived state is introduced.
See [implementation/evaluation evidence](docs/evidence/conversation-history-ch6.md).
The 24-case paired synthetic check found no required-context losses; aggregate cost/
latency was higher, so broad enablement remains unjustified. Oria stays on recent;
provider publication and consumer adaptive configuration remain rollout work.

### CH7/OCH5 repeat release — 2026-11-11

Published/deployed provider 0.2.3 and Oria 0.2.4 with CH4–CH6 included, recent
replay active and observation/adaptive disabled. Local/CI gates, verified images,
live continuity/topic/erasure and capture rollback passed. See
[repeat-release evidence](docs/evidence/conversation-history-ch7-repeat.md).
Keep private Telegram review and workload-specific adaptive enablement open.
