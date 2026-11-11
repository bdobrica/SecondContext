# Conversational learning (CL1–CL2)

CL1 implements opt-in background learning from ordinary conversations. It stores
simple observations, independent of the existing numeric person/belief modeling
pipeline. CL2 adds natural request interpretation and evidence selection for fresh
conversations and follow-ups. CL3 and the real-model M1 usefulness demonstration
remain open; deterministic checks establish wiring, not better real advice.

## Enable and inspect

Apply migration **000006** with `make migrate-up`, then start the gateway normally.
The API process starts a background queue consumer; no extra service is required.
Learning is **off by default** and is enabled separately for each authenticated
subject. Deploying this code or applying the migration does not enable learning.

Open `/memory/inspector` on the gateway origin. Enter a personal API credential,
or a service credential and its permitted `X-SecondContext-Subject`. Enable
learning once. Connect an ordinary chat client with endpoint, credentials, and
model configuration. Personal credentials require no per-turn memory metadata,
session headers, or ingestion API. Service credentials still require their
existing authenticated subject delegation; a shared key never identifies a chat.

The inspector lists conversational observations from the most recent 100 memories,
with original source, speaker, first-observed date, evidence classification, time
scope, expiry, and correction date. It also shows the last 100 capture receipts,
offers pause/resume, retry, summary correction, and forgetting. Credentials stay
in page memory, not local storage. The HTML/JS shell is public; all personal data
and control endpoints require authentication. There are no external page assets.

Equivalent API operations, all scoped to the authenticated subject:

| Endpoint | Behavior |
| --- | --- |
| `PUT /memory/learning` with `{"enabled":true}` | Enable future capture for this account. |
| `PUT /memory/learning` with `{"enabled":false}` | Pause and cancel unfinished capture; retain existing completed memories. |
| `GET /memory/learning` | Setting and recent capture status; no source text in queue status. |
| `POST /memory/learning/retry` with `{}` | Requeue failed captures, never completed/suppressed receipts. |
| `GET /memory?limit=100` | Existing list API, including source and provenance metadata. |
| `GET /memory/evidence?ids=<comma-separated UUIDs>` | Inspect up to six selected, currently retrievable observations owned by this subject. Foreign, forgotten, expired and fenced records are omitted. |
| `PATCH /memory/{id}` with `{"summary":"Alex now prefers comprehensive review requests."}` | Correct a conversational observation and reindex it. Other memory sources retain their existing APIs. |
| `DELETE /memory/{id}` | Forget the observation, preserving its occurrence receipt against replay. |

Summary correction preserves the original source and records a user correction;
it does not edit the original transcript, people/topic labels, or expiry. Full
natural-language correction, source-derivative management for older modeled
memories, search UI, and editable time scopes remain CL3 work.

## Capture and retrieval controls

| Control | New conversational capture | Personal-memory retrieval | Reference knowledge |
| --- | --- | --- | --- |
| Account learning enabled | Eligible input queued | Existing behavior | Existing behavior |
| Account learning paused | Suppressed; unfinished work canceled | Still available | Unchanged |
| `disable_memory:true` | Still queued when learning is enabled | Disabled, including person/belief/scenario context | Unchanged |
| `temporary_chat:true` | Suppressed | Disabled | Unchanged |
| `disable_knowledge:true` | Unchanged | Unchanged | Disabled |

`temporary_chat` is supported at the top level or in `metadata` on Chat
Completions and Responses, including managed requests. It is a per-request
control; clients must send it for every request in a temporary conversation.
These controls do not alter existing dialogue persistence/replay or erase dialogue
held and supplied by the client. Temporary chat is a personal-memory control,
not a promise of zero server transcript retention. Disable managed history
separately when it is unwanted.

## Natural recall, advice and follow-ups (CL2)

After capture completes, start a **fresh conversation under the same identity** and
ask “Help me ask Alex for review.” No old transcript, session ID, `people`, `topics`,
`goal`, or `memory_mode` is needed. Continue with “Make it warmer” or “What are two
other approaches?” Both Chat Completions and Responses use the selected memories
to guide the ordinary answer. Alternative approaches request qualitative tradeoffs,
not numeric success predictions. A draft is never treated as a sent action.

The request interpreter sees only the latest user message and up to six preceding
user/assistant messages (at most 3,000 UTF-8 bytes per message and 10,000 total).
It selects which preceding messages are needed to understand the current task,
then produces a focused query, goal, people/topic hints and answer intent. A topic
switch should select no old dialogue. Original messages, tool blocks, structured
output options and streaming formats still reach the generator unchanged apart
from the added system context. Tool/system/developer content is excluded from
identity interpretation. Managed requests may use only bounded, canonically
accepted same-session dialogue; pending/rejected drafts are excluded.

Explicit metadata values, including empty arrays, override inferred values for
that field. Explicit `memory_mode` retains its existing endpoint restrictions and
legacy scenario behavior. Inferred topics are hints rather than mandatory filters.
The ordinary path uses source-backed observations directly, without loading numeric
person/belief models. Explicit people/topics/mode metadata keeps the legacy model
context path available.

Identity resolution checks live subject-owned memory labels and stored aliases
attached to those labels. An alias is supported only if already recorded; the
interpreter cannot create aliases or merge entities. Exact full names and unique
first-name references can resolve. Shared aliases/first names, such as “Alex” when
both Alex Chen and Alex Morgan are known, withhold personal memory and prompt for
clarification. An unfamiliar full name is not silently collapsed onto a known first
name. Expired/forgotten observations do not supply labels. Identity lookup is bounded
to 1,000 live labels; overflow/failure withholds personal recall rather than guessing.
This does not establish identity for indistinguishable people stored under the same
label; correcting such labels and richer entity management remain outside CL2.

Hybrid retrieval uses the current-task query with resolved names, followed by a
bounded model selection of useful candidate IDs and brief relevance reasons. Only
returned candidate IDs can be selected. Subject, expiry, capture-completion and
source-deletion checks remain authoritative. At most six memories enter the final
context, subject to existing prompt budgets. The interpreter and evidence selector
each have a four-second deadline using `OPENAI_CHAT_MODEL`. They add model calls
before generation (including before SSE starts); answer usage remains the provider's
answer usage, not a total bill for these extra calls. Measure real latency/cost
before assuming an unchanged experience.

Invalid/failed interpretation falls back to the latest user text, never a
concatenation of all old user messages. Invalid/failed evidence selection falls
back to ordinary semantic results after identity checks. These fallbacks preserve
answer availability but can be less relevant; topic-switch quality and subtle
reference resolution still depend on model behavior. `disable_memory` and
`temporary_chat` skip both interpretation and personal-memory selection. Reference
knowledge controls remain independent.

### Inspect the evidence used

Ordinary Chat/Responses answers expose `X-SecondContext-Memory-Ids` (comma-separated
IDs actually retained in the prompt), `X-SecondContext-Interpretation`, and
`X-SecondContext-Evidence`. These are response headers, including on SSE, not extra
answer/tool events. Empty IDs mean no personal observations were included.
Interpretation reports `inferred`, `fallback`, `identity_unavailable`, or `disabled`;
evidence reports `selected`, `semantic_fallback`, `empty`, `identity_unresolved`,
`unavailable`, or `disabled` when applicable.

Paste the IDs into **Evidence used in an answer** in `/memory/inspector`. This uses
`GET /memory/evidence` and shows current source text, date and summary. It does not
save an extra copy of the claim: corrections, expiry and forgetting affect inspection
too. It identifies records supplied to the generator, not proof the model used
every record. Responses additionally returns `metadata.context_packet` with the
focused query, inferred goal/labels/intent, ambiguity, selected evidence, provenance
and relevance reasons. Reasons are model explanations of selection, not new facts.

Managed first-generation answers expose the same headers. Cached managed Chat
receipts retain their original body but do not reconstruct these headers; keep the
first response's IDs. Managed Responses saves its context packet with the existing
immutable response receipt. As with client-held chat history, old saved responses
can retain historical text; inspect current evidence to see subsequent changes.
The managed configuration recipe changes with CL2, so the existing configuration
conflict rules apply to turns reserved under an older recipe. CL2 needs no migration
beyond CL1's 000006, and adds no runtime deployment or learning enablement.

## Eligible evidence

After successful ordinary Chat Completions or Responses generation, user messages
are handed to a durable queue. Chat streaming emits its answer and `[DONE]` before
the durable handoff; extraction and embeddings never run on the streaming path.
Transport failures and incomplete streams do not enqueue ordinary capture.
A successfully terminated completion (including a tool-call or length finish)
can contribute its **user input**, never its generated assistant response.

Only user text reaches the extractor. System/developer instructions, assistant
text, tool arguments, and tool results are not evidence for learned facts. Tool
structure participates in occurrence identity but is not sent for extraction.
The extractor is instructed to return zero to sixteen useful, separate explicit
reports: preferences, observations, facts, constraints, decisions, commitments,
and reported outcomes. Empty output is valid. Small talk, generic questions,
hypothetical/fictional examples, unsupported inference, and requests to draft
actions should return no observations. Inferences are deliberately not stored
in this first implementation. User reports are not independent verification.

Each result requires an exact source quote, type, summary, people/topics,
`explicit_report` classification, time scope, and an optional absolute expiry.
Validation rejects unsupported quotes/people, invalid types, duplicate claims,
missing fields, duplicate JSON members, and malformed output. Prompt adherence
on arbitrary real conversations still requires live-model evaluation; structural
validation does not prove the truth of a summary or detect every hypothetical.

Each observation retains its supporting source excerpt, rather than copying the
whole multi-observation message into every memory. The queued full message is
cleared on completion or suppression.

First-observed timestamps belong to the capture receipt and never refresh on
retry. Event dates/time scopes remain separate. Expired observations are excluded
from ordinary recall. Simple observations are embedded/indexed for existing
retrieval, without invoking person scoring, belief updates, or scenario prediction.
The legacy single-record `/memory/extract` contract remains unchanged; automatic
conversation learning uses the new zero-or-many extractor.

Managed generated turns become eligible only after canonical acceptance.
Explicit consumers' pending drafts, rejected/abandoned turns, failed attempts,
and expired turns never reach extraction. Their capture receipts wait for
acceptance; abandonment/expiry clears the queued source. Accepted local turns
contribute their user text, not the accepted assistant text. Learning controls
are pinned when a managed capture receipt is first created; later retries and
finalization cannot turn a suppressed receipt into eligible evidence.

## Repeated history, retries, and forgetting

Supplied-history occurrence identity combines the optional session identifier
with the ordered prefix of user/assistant/tool dialogue. System instructions
are excluded. Repeated prefixes converge on one subject-owned receipt; two
identical reports at different positions or after different dialogue remain
distinct. Managed identity uses session plus turn ID. Capture receipts remain
after deleting individual observations. Pause/temporary requests record suppressed
occurrence hashes without source text, so resending that prefix does not learn it
later. Temporary requests do this even before learning has ever been enabled.

There is an unavoidable ordinary-client ambiguity: identical fresh transcripts
without stable conversation/message IDs cannot reveal whether they are retries or
distinct events. They are conservatively treated as the same evidence. A distinct
session ID or a new report in extended dialogue disambiguates them. Trimming or
rewriting an earlier prefix, changing session IDs, or reporting the fact again can
create a new occurrence. Forgetting prevents matching old-prefix replay; it is
not a global ban on ever learning the same text again. History first supplied
after enablement can be learned even if it originated before enablement.

Deleting/correcting an observation from a partially failed batch cancels the
remaining capture receipt so retries cannot recreate or overwrite it. Unfinished
batch rows may remain inspectable but are excluded from recall; a successfully
corrected row is ready independently. Completed batches keep their replay receipt.
There are no person/belief derivatives for automatic observations. Linked-source
deletion removes associated capture records, canonical observations, and vector
points. Durable source fences suppress recall even while cleanup is incomplete.
Whole-subject purge cascades the settings/queue and uses the existing vector purge.

## Progress and recovery

Capture statuses are `pending`, `waiting_acceptance`, `completed`, `failed`, and
`suppressed`. A completed capture may have produced zero observations. Do not
assume an immediate fresh conversation can recall input while capture is pending
or failed. The current client-supplied dialogue can still inform the answer.

The worker polls every second, runs at most one extraction per process at a time,
and uses cross-process capture locks. It extracts/embeds outside the subject lock,
then rechecks consent, acceptance, source fences, and deletion before publishing.
The short indexing/publication stage shares the existing subject lock and can
briefly delay another operation for that subject. A batch becomes retrievable only
after indexing completes. Stored extraction output and per-observation idempotency
keys make partial-index retries converge without new evidence or new dates.

Failures expose a safe `capture_failed` code, retry after 30 seconds, and stop
automatic attempts after three failures. The inspector/API can retry failed work.
Pending work resumes after process restart. Pausing during extraction prevents
publication; it cannot retract an already-started provider request. Capture
attempts have a 90-second deadline. No source/provider bodies enter capture logs.

`X-SecondContext-Capture` reports handoff status (`pending`, `paused`, `temporary`,
`disabled`, `unchanged`, or `failed`). For SSE it is an HTTP trailer, which some
clients/proxies do not expose. It is not a recall-ready receipt: use the inspector
for canonical status. Handoff failure preserves answer delivery; retry the same
request to enqueue its evidence. A crash before a successful handoff similarly
requires resending the input. Managed canonical acceptance remains authoritative.

## Validation and remaining evidence

Focused tests cover zero/multiple observation contracts, provenance, repeat-prefix
reconciliation, distinct occurrences, ordinary Responses/Chat/SSE, incomplete
streams, all acceptance modes, restart, partial indexing/retry, correction,
forgetting, expiry, pause/purge during extraction, linked-source erasure, and
subject isolation. A deterministic generator echoes the assembled prompt to prove
that fresh-chat context changes after correction/deletion. It does **not** establish
that a real model produces better advice. No live provider credential was available
for this implementation's checks; real extraction and the M1 Alex demonstration,
matched comparisons, and dogfooding remain outstanding.

Recorded on 2026-11-11: `make verify`, `make test-docker` (schema 6), focused CL1
race tests, and inspector JavaScript syntax checks passed. Inspector HTTP tests
verify that the shell is public while memory/control APIs require authentication.
No browser interaction/visual test or live-provider evaluation was performed.

Run `make verify` for mandatory static/unit/integration checks and `make test-docker`
for packaged migrations/runtime checks. Open the inspector and use the Alex
demonstration in [PLAN.md](../PLAN.md#product-promise-and-first-milestone) with a real
provider before declaring CL1–CL3/M1 usable.

CL2's focused protocol fixtures cover fresh Chat/Responses recall, natural drafts
and alternatives, bounded follow-ups, unrelated topic switches, full names/shared
first names/stored aliases/unknown names, explicit metadata overrides, malformed
analysis fallbacks, correction/forgetting, expiry and isolation. The unchanged
OpenAI SDK acceptance script is `scripts/test-conversational-recall-sdk.py`, invoked
by `TestNaturalContextOpenAISDK` when `SECOND_CONTEXT_CHAT_SDK_PYTHON` points to a
prepared interpreter. It uses a deterministic provider and background capture;
it makes no custom memory calls. Real-provider extraction/retrieval/advice quality,
actual user effort and latency still need the M1 demonstration and dogfooding.

Recorded for CL2 on 2026-11-11: `make verify` passed with the mandatory database/vector
suite and both unchanged-client scripts using OpenAI SDK 3.48.0; `make test-docker`
and inspector JavaScript `node --check` passed. Isolated test stacks were cleaned
up. The inspector evidence route passed authentication and ownership checks; no
browser interaction or real-provider evaluation was performed.
