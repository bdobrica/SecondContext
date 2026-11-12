# Conversational learning (CL1–CL4)

CL1 implements opt-in background learning from ordinary conversations. It stores
simple observations, independent of the existing numeric person/belief modeling
pipeline. CL2 adds natural request interpretation and evidence selection for fresh
conversations and follow-ups. CL3 adds searchable inspection and explicit natural
memory controls. CL4 compares new reports with prior observations and updates the
current working view while preserving evidence history. The real-model M1 usefulness demonstration remains open;
deterministic checks establish wiring, not better real advice.

## Enable and inspect

Apply migrations through **000008** with `make migrate-up`, then start the gateway normally.
The API process starts a background queue consumer; no extra service is required.
Learning is **off by default** and is enabled separately for each authenticated
subject. Deploying this code or applying the migration does not enable learning.

Open `/memory/inspector` on the gateway origin. Enter a personal API credential,
or a service credential and its permitted `X-SecondContext-Subject`. Enable
learning once. Connect an ordinary chat client with endpoint, credentials, and
model configuration. Personal credentials require no per-turn memory metadata,
session headers, or ingestion API. Service credentials still require their
existing authenticated subject delegation; a shared key never identifies a chat.

The inspector searches current conversational summaries and people/topic labels,
with 50 observations per page,
with original source, speaker, first-observed date, evidence classification, time
scope, expiry, and correction date. It also shows the last 100 capture receipts,
offers pause/resume, retry, summary correction, and forgetting. Each observation
shows whether it is ready for recall, expired, or unavailable. A fresh-question
form has a per-question temporary control and displays selected memory evidence,
selection reasons and supplied reference-document excerpts. Each submission starts
fresh; it does not resend the previous question or answer. Credentials stay
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
| `GET /memory/observations?q=<phrase>&offset=0` | Search current conversational summaries/labels or an ID, 50 per page; returns `has_more`, `offset`, and per-row `recall_status`. Search is a literal case-insensitive substring, not semantic retrieval. Query max 300 bytes; offset 0–1,000,000. Linked-source fences are respected. |
| `GET /memory/evidence?ids=<comma-separated UUIDs>` | Inspect up to six selected, currently retrievable observations owned by this subject. Foreign, forgotten, expired and fenced records are omitted. |
| `PATCH /memory/{id}` with `{"summary":"Alex now prefers comprehensive review requests."}` | Correct a conversational observation and reindex it. Other memory sources retain their existing APIs. |
| `DELETE /memory/{id}` | Forget the observation, preserving its occurrence receipt against replay. |

Summary correction preserves the original source and records a user correction;
it does not edit the original transcript, people/topic labels, or expiry. Correction
time is included separately in later retrieval context. Relabeling and editing time
scopes remain outside this summary-edit workflow; forget the misattributed record
and report the corrected fact in a new conversation occurrence when needed.

## Natural memory controls (CL3)

An unchanged chat client can send these as a complete latest user message:

- “What do you remember about Alex?” or “Show my memories”.
- “Correct memory Alex to Alex now prefers comprehensive review requests.”
- “Forget memory Alex” or “Forget what you remember about Alex”.
- “Pause learning” and “Resume learning” (also “Stop learning” / “Enable learning”).

Targets match current conversational summaries/labels by literal substring, or an
exact memory ID. One match permits correction/forgetting; multiple matches cause
no mutation and return IDs for clarification. Repeat with a specific ID, for
example “Forget memory <ID>”. Inspection/clarification lists at most ten summaries,
with source, date, evidence and expiry; use inspector pagination for more. Zero
matches cause no change. The original source excerpt remains available in the
inspector, including after a correction. These commands manage conversational
observations; older manually ingested records retain their existing APIs.

This is an explicit, bounded English command grammar, not arbitrary model-generated
mutations. Optional “Please”, “Can you”, or “Could you” prefixes are accepted.
Multiline, quoted, hypothetical, old user, assistant and tool messages cannot
execute a command. A tool continuation does not repeat a preceding command.
Pronouns such as “it”/“that” require a specific target. Other paraphrases may fall
through to the assistant, which is instructed to direct the user to the inspector
or supported commands and never claim it performed a mutation. Broader natural
corrections and changing beliefs are learned asynchronously by CL4 as separate
source-backed observations and inferred relations. Models do not choose explicit
command targets or perform deletion, settings changes, or direct summary edits.

Recognized commands receive a local receipt on both endpoints, including Chat SSE
and its optional usage chunk. No interpretation/generation call is needed; summary
correction still requires an embedding and successful indexing. Usage is zero for
the local answer, excluding that embedding. Failure returns an API error without a
success receipt. Commands are suppressed from background learning, including when
old transcripts are resent. Other ordinary user statements in the same transcript
remain independently eligible. A command executes before delivery of its receipt;
a disconnect cannot undo an already completed operation. Ordinary phrase targets
are re-evaluated on each request; use a stable ID for retries.

`temporary_chat` or `disable_memory` blocks natural inspection and mutation for
that request and returns directions to the inspector. Managed generated turns
retain exact cached receipts. Managed **explicit-acceptance** turns may inspect,
but cannot mutate; their response directs the user to the inspector or ordinary
chat. Accepting an assistant draft does not authorize a memory operation. Command
requests requiring JSON/schema output or forced tool calls are rejected with
`memory_command_requires_text` before mutation. Normal requests retain their
existing structured/tool behavior. The CL3 managed recipe changes, so existing
configuration-conflict rules apply to turns reserved by older code.

Migration 000007 adds database triggers using existing evidence links. Correcting
summary/labels/expiry or deleting a memory invalidates the entire dependent
person/topic aggregate, belief, or graph edge rather than retaining stale claims or
confidence. Unrelated aggregates survive. Linked-source and subject deletions also
pass through these triggers. Automatically captured observations still create no
numeric derivatives; the invalidation covers older/explicitly attached models as
well. Independent outcome reports and client/server saved dialogue are not erased
by forgetting a memory. Source-free manually supplied model values cannot be
attributed to a memory and are not removed by this dependency rule.

## Natural outcomes and belief updates (CL4)

With learning enabled, report results or corrections in ordinary Chat Completions
or Responses. No `/interactions/outcome` call, person score, or custom metadata
is required. For example:

1. “I sent Alex a request to review the API section of the launch proposal.”
2. “Alex agreed, but only to review the API section of the launch proposal.”
3. In a fresh conversation, ask “How should I follow up with Alex?” after capture
   completes. The generator receives the dated, limited outcome and its evidence link.

“I had that backwards: Alex prefers a detailed review brief, not a terse request”
can supersede a remembered short-review preference. “Alex is now available after
finishing the incident” can update an earlier unavailable report without marking
it as a contradiction. These are learned reports, not immediate command receipts:
background processing must finish before later conversations use the change.

Each observation remains a separate source-backed record. The working belief is
the set of current observations plus classified relations, independent of the
legacy numeric `/beliefs` pipeline. Relation kinds have these effects:

| Kind | Working view |
| --- | --- |
| `agreement` | Retain both reports and dates; do not increase confidence or claim independent verification. |
| `contradiction` | Retain both as `disputed`; later prompts warn about uncertainty even if only one side fits the context budget. |
| `correction` | Retain the earlier source as `superseded`; use the later corrected claim. |
| `changed_circumstances` | Retain the earlier source as `superseded`; use the later time-scoped claim, not a permanent personality trait. |
| `outcome_of` | Link a reported result to one evidenced `reported_action`, without a success score or causal inference. |
| `unrelated` | Keep independent observations; store no relationship. |

A reported action requires a user statement that it actually occurred. Drafts,
assistant suggestions, hypothetical successes, and commitments are not sent
actions. An ambiguous result stays unlinked. The answer instructions ask for
clarification only when knowing which request it concerns matters to the answer.
Natural outcomes do not create legacy `interaction_outcomes` scores or attribute
success to a suggested strategy.

The inspector and exact evidence endpoint expose `metadata.working_state`
(`current`, `disputed`, `superseded`) and `metadata.relations`, including both IDs,
the classification, supporting quotes and explanation. `recall_status` separately
reports indexing/expiry readiness; a ready but superseded observation is historical
and excluded from ordinary recall. Natural inspection includes states and links.
Both endpoints' context packets include `working_state` and bounded relation IDs.
Search follows forward evidence links, so finding obsolete wording can retrieve
its replacement even when that paraphrase was not in the initial vector hits.
Original observations remain searchable in the inspector; ordinary recall selects
the current view rather than answering arbitrary historical-state queries.

Migration **000008** stores subject-scoped relations with cascading evidence
references. Correcting either endpoint invalidates its inferred relations;
forgetting either endpoint removes those links. This can make a retained older
observation current again: inspect or forget that record too if it is no longer
wanted. Source deletion and whole-subject purge remove dependent relations.
Expired replacements do not automatically restore an explicitly superseded claim.
Supersession or dispute invalidates source-dependent legacy person models, beliefs
and graph aggregates, so explicit structured-model requests cannot reuse them.

Comparison is bounded: hybrid retrieval ranks up to 24 candidates; at most eight
older, eligible conversational records are compared per new observation. Each
candidate receives a strict decision with an allowed ID and exact supporting
quotes. Different person-label sets cannot be linked; aliases and pronouns are not
automatically merged. With no people, a shared topic is required. These gates
prevent unsupported IDs and identity merges but cannot verify a model's semantic
judgment. The classifier must distinguish scope, explicit retraction, ambiguous
actions, and temporal change; future changes are retained independently until
reported as current, not scheduled for automatic activation.

The observation extractor bounds summaries to 2,000 bytes, evidence quotes to
4,000 bytes, time scopes to 300 bytes and each of at most 16 people/topic labels
to 200 bytes. Oversized output fails validation rather than growing comparison
prompts without a bound.

Comparisons, like extraction, run asynchronously. Malformed or unavailable model
comparisons leave the capture failed/retryable and do not publish a partial working
update. Relations and capture completion publish atomically after indexing and
consent/source checks. Background workers serialize comparisons per subject on a
separate lock, while ordinary chat and inspector edits remain available. Edited
or deleted candidate snapshots are discarded at publication. Retries do not
refresh evidence dates or duplicate relationships. Late reports use first-observed
time for ordering; an old failed capture cannot supersede newer evidence or an
explicit correction. There is no retrospective sweep to link older captures that
finished after newer ones, no same-message relation pass, and no comparison with
manual/legacy memory sources. Large relation expansions stop at 200 records and
fail recall rather than returning obsolete claims. These are bounded retrieval
and model-classification behaviors, not exhaustive identity or truth resolution.

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
`unavailable`, or `disabled` when applicable. CL3 local command receipts report
interpretation `memory_command` and evidence `disabled`.

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
Automatic observations do not create numeric person/belief derivatives. Migration 000007
invalidates any aggregates explicitly linked to corrected/deleted evidence. Linked-source
deletion removes associated capture records, canonical observations, and vector
points. Durable source fences suppress recall even while cleanup is incomplete.
Whole-subject purge cascades the settings/queue and uses the existing vector purge.

## Progress and recovery

Capture statuses are `pending`, `waiting_acceptance`, `completed`, `failed`, and
`suppressed`. A completed capture may have produced zero observations. Do not
assume an immediate fresh conversation can recall input while capture is pending
or failed. The current client-supplied dialogue can still inform the answer.

The worker polls every second, runs at most one extraction per process at a time,
and uses cross-process capture and learning-subject locks. It extracts, compares
and embeds outside the interactive subject lock,
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
`disabled`, `unchanged`, `suppressed`, or `failed`). `suppressed` covers newly
recorded command-only input that is ineligible for extraction. For SSE it is an HTTP trailer, which some
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

CL3 verification completed on 2026-11-12: `make verify` passed with mandatory
Postgres/Qdrant integration checks and both unchanged OpenAI SDK scripts. The CL3
sequence demonstrated changed fresh-answer context after correction and absence
of personal evidence after forgetting, on both endpoints. Tests also covered
ambiguity, failed indexing without a success receipt, dependent-summary
invalidation, search pagination/expiry, isolation, controls and managed acceptance/
retries. `make test-docker` passed on schema 7. Inspector JavaScript syntax and
final source/documentation checks passed; isolated stacks were cleaned up.
No real-provider or browser-interaction test was performed. No deployment or live
learning enablement occurred. Live M1 acceptance and matched usefulness comparisons
remain open.

CL4 verification completed on 2026-11-12: `make verify` passed, including mandatory
Postgres/Qdrant integration and all three unchanged OpenAI SDK scripts. The new
`scripts/test-conversational-updates-sdk.py` is invoked by `TestCL4OpenAISDK` with
the same prepared `SECOND_CONTEXT_CHAT_SDK_PYTHON` interpreter. It uses only normal
Chat/Responses and text inspection to demonstrate background correction, action/
outcome capture, changed fresh-chat context and retained historical evidence.
Other CL4 tests cover time-scoped availability, ambiguous actions, unrelated
similar claims, agreement without reinforcement, disputes, retry, concurrent
correction/forgetting/pause/purge, subject isolation and legacy belief invalidation.
`make test-docker` passed with migration 000008. Inspector JavaScript syntax and
source/documentation checks passed; disposable stacks were cleaned up. No live
provider credential was available and no browser-interaction test was performed.
These checks prove context wiring and evidence lifecycle, not real-model semantic
classification or more useful advice. Live CL4 acceptance, latency/effort measures,
matched comparisons and M1 remain open. No deployment or live enablement occurred.
