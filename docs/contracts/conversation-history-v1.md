# Conversation history — v1

Status: implemented by CH3, 2026-11-09; disabled by default until configured.
CH2 implements the ordinary [Chat Completions subset](chat-completions-v1.md).
CH3 implements managed capture/replay, capabilities and finalization; see
[configuration, recovery and rollout](../conversation-history.md).
[ADR 0007](../adr/0007-conversation-history.md) authorizes this additive boundary.
CH4/CH5 observation and CH6 adaptive selection are independent, disabled-by-default
features; see [adaptive operations and evaluation gates](../adaptive-history.md).
The authored [JSON Schema](conversation-history-v1.schema.json) defines closed
context endpoint bodies. [Conformance cases](conversation-history-v1.examples.json)
are synthetic structural fixtures, not evidence of server lifecycle behavior.
The prose defines authorization, headers, state transitions and semantic validation.
MUST/MUST NOT below are normative requirements under RFC 2119/8174.

## Compatibility and ownership

SecondContext owns transcripts, ordering and final prompt assembly. The application
owns consent, canonical facts, input preparation, accepted replies and delivery.
Service/user authentication and body identity matching follow
[service-context v1](service-context-v1.md). A session/turn/response ID is never
authority. All new endpoints require authenticated storage and hold the existing
subject lock; no service credential gains general transcript CRUD or GET access.

| Caller | Dialogue source | Required lifecycle |
| --- | --- | --- |
| Ordinary Chat Completions | Supplied `messages`, preserved in order | None beyond the standard request |
| Existing Responses client | Existing `input` subset and session metadata semantics | Unchanged |
| Managed client | Current input plus eligible server history | Explicit headers; finalize only in explicit acceptance mode |

Ordinary clients configure base URL, bearer and model only. Multiple chats using one
subject MUST NOT be merged. Supplied history MUST NOT be silently supplemented or
trimmed by Decisions. Storage of supplied messages does not make them eligible for
managed replay. Existing `/v1/responses`, `/v1/models`, memory and linked-context
contracts remain compatible; no new strict parser is retrofitted onto legacy
Responses requests that do not use the extension.

CH2 must support text messages with system/developer/user/assistant/tool roles,
function calls/results and non-streaming and SSE Chat Completions. It must preserve
call IDs, finish reasons, usage, chunk IDs/deltas and terminal stream semantics.
It must publish and test the exact parameter/model matrix (including generation
controls, tool choice, structured output and usage options), rejecting unsupported
behavior-affecting fields with `unsupported_feature`; no blanket OpenAI parity.
Image/audio inputs, hosted tools and general completion CRUD are not required by
this contract. Existing Responses streaming remains unsupported until separately
implemented. This boundary follows the [official Chat Completions reference](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)
(reviewed 2026-11-09); standard bodies are not redefined by our context schemas.

## Generation headers and input profile

Header names are case-insensitive; values are case-sensitive ASCII. Reject duplicate
instances, comma lists, empty values and unknown enums with HTTP 400
`invalid_history_request`. HTTP optional surrounding whitespace is stripped.

| Header | Values/default |
| --- | --- |
| `X-SecondContext-Session` | Opaque `[A-Za-z0-9][A-Za-z0-9._:-]{0,127}`; no default |
| `X-SecondContext-History` | `off` (default), `recent`, `adaptive` |
| `X-SecondContext-Turn` | Canonical lowercase UUID; no default |
| `X-SecondContext-Acceptance` | `generated` (default), `explicit` |

An existing nonempty `metadata.session_id` is an alternate session selector. When
both selectors occur they MUST match exactly; otherwise 400 `session_conflict`.
Legacy metadata-only IDs retain their existing syntax when no managed turn is
requested. A managed turn requires a valid session, turn ID and authenticated
storage, even for `off`. `explicit` or non-off history without a turn ID fails.
Requesting an unadvertised mode returns 400 `unsupported_feature`.
A turn ID with `off` is permitted: capture the new turn without replaying older
turns. No turn ID with `off`/generated is the legacy/supplied-dialogue path, even
with a session selector. Memory/knowledge switches do not control history.

Managed v1 is deliberately text-only: exactly one current user string (Responses
`input` string; Chat Completions a final user message with string `content`), with
optional preceding current system/developer text instructions on Chat Completions.
No prior user/assistant/tool messages, tools/tool choice, multimodal content,
multiple choices (`n` other than 1), or managed streaming in v1. Reject these with
400 `unsupported_history_input`; tools and SSE remain supported on the ordinary
Chat Completions path. This prevents ambiguous merging and partial tool groups.
Instructions/current calculated facts are not copied into replayable history.
Managed text bounds: current user 4,096 Unicode characters, accepted assistant
16,384; strings must contain non-whitespace text. Finalize bodies max 128 KiB;
capability request max 4 KiB, response max 16 KiB. Reject invalid Unicode and
duplicate JSON keys. These new bounds do not narrow legacy generation limits.

A successful managed generation echoes these headers, without changing standard
response bodies: `X-SecondContext-Contract: conversation-history-1`, exact Session
and Turn headers, and `X-SecondContext-Snapshot` (decimal integer >=0). Its standard
body ID is the response ID. No raw history, routing confidence or provider details
are exposed in diagnostic headers. A response ID belongs to exactly one resolved
subject/session/turn. A generated draft in explicit mode is not a receipt.

## Capability discovery

`POST /v1/context/capabilities` accepts exactly `{ "user": "<resolved subject>" }`.
HTTP 200 returns `Capabilities` in the schema. `contract_version:1`, implementation
`conversation-history-1`, status `ready`, exact user, the two generation endpoint
paths, `managed_input:"single_user_text"`, acceptance modes and enabled history
modes are required. Baseline modes are `off,recent`; advertise `adaptive` only after
its evaluation/release gate. Lists contain unique values; order has no meaning.
`finalize_actions` contains `accept_generated,accept_local,abandon`.
`history_coverage:"linked_sessions"` affirms lifecycle coverage, not delivery.
Limits include recent exchange cap (initial baseline 8), managed input/reply bounds,
whole prompt input budget and reserved output tokens (positive, model-specific
safe deployment ceilings), and `pending_ttl_seconds:86400`.

Disabled/unready managed storage returns 503 `history_unavailable`, never a ready
shape. Unsupported endpoints/versions are not capability evidence. Check actual
implementation and coverage, not health. Generation disablement need not disable
capability/finalize/erasure recovery. Recheck capability after deployment changes;
clients may cache readiness only within the current process/configuration epoch.

## Finalization and exact receipts

`POST /v1/context/turns/finalize` accepts one of the closed schema variants:

| `action` | Required fields beyond contract_version/user/session_id/turn_id |
| --- | --- |
| `accept_generated` | `response_id`, `assistant_text` (original or validated replacement) |
| `accept_local` | `user_text`, `assistant_text`, `knowledge_refs` (array, possibly empty) |
| `abandon` | No text or response ID; terminates an existing pending turn |

Local acceptance is an application-origin exchange, not a way to overwrite an
existing generated turn. It atomically reserves and accepts an unused turn under
the subject/session lock. At most twenty unique RecordRefs (entity/event/reading,
canonical lowercase UUID, positive integer revision) use the existing linked-context
semantics. Revalidate current projection and canonical source links before accepting.
Generated acceptance inherits all dependencies already bound before generation;
it cannot remove/add references. Replacements must not introduce unlinked canonical
sources. Link local sources before exposing any accepted text to replay.

HTTP 200 returns exactly the `Receipt` schema: contract_version, exact user,
session_id, turn_id, action, status (`accepted` or `abandoned`), sequence,
response_id (generated acceptance only), and `history_coverage:"linked_sessions"`.
Sequence is a positive safe JSON integer (<=9007199254740991). The initial receipt
and every identical retry return the same values; it does not include transcript
text. Verify all identities, action, status and coverage, not just 2xx. An accepted
receipt is neither an erasure receipt nor evidence that an end user saw the reply.

## Immutable binding, ordering and recovery

The durable key is `(resolved subject, session_id, turn_id)`; session ownership
checks still apply, including existing globally unique session IDs. Reserve a
strictly increasing per-session sequence under the subject lock before generation.
Gaps are allowed; timestamps never order turns. Only one unresolved managed turn
may exist in a session. A different turn returns 409 `turn_pending`; supplied
legacy calls do not bypass this barrier to create managed history.

Generation identity binds the JSON object `{contract_version:1, user, session_id,
turn_id, endpoint, history, acceptance, body}` after selector/header normalization.
`body` is the complete caller JSON object before retrieval/prompt assembly; no fields
are silently removed. Compute SHA-256 over UTF-8 [RFC 8785 JCS](https://www.rfc-editor.org/rfc/rfc8785)
canonical bytes. JSON whitespace/key order are immaterial; strings and array order
are preserved, absent versus explicit defaults remain distinct. Reject duplicate
keys, invalid Unicode and non-I-JSON numbers. Finalization separately binds the
entire parsed finalize body by the same scheme. Hashes stay internal, never logged
or used as authority. Clients persist exact prepared requests rather than rebuild
facts or timestamps for a retry. Upstream resolution/configuration is pinned with
the reservation; an unresumable configuration fails, never silently reinterprets it.

At reservation capture `snapshot_sequence`, the highest earlier accepted sequence
(or zero), and the session's internal context generation. Persist the selected turn
identities before the first generator attempt so retries cannot extend their snapshot.
Adaptive selection MUST commit once, including an empty selection, before generation;
retries after that commit MUST reuse it without reclassification. Before every provider attempt
and accepted-result retry, revalidate subject/session fences and all source revisions;
invalidated snapshots fail rather than serving stale content. Do not persist full
instructions/calculated facts merely to retain the hash: a retry supplies the same
body; only prepared dialogue, necessary draft/result and dependency state are stored.

| State/event | Result |
| --- | --- |
| New valid generation | Reserve sequence/snapshot; enter pending |
| Same request concurrently in flight | 409 `turn_in_progress`; no second simultaneous provider call |
| Durable complete result, matching generation retry | Same response ID/body/snapshot; no provider call |
| Upstream failure/crash before durable result | Same bound request may resume after worker lease recovery |
| Complete result, generated acceptance | Atomically accepted and eligible; no finalize call needed |
| Complete result, explicit acceptance | Pending draft; only accept_generated or abandon resolves it |
| Matching accept_generated | Atomically store accepted text and receipt; rejected draft never replayed |
| Matching local acceptance | Atomically reserve/accept complete prepared exchange |
| Pending abandon or expiry | Terminal abandoned; discard draft/request content; unblock next turn |
| Changed request/finalization at same key | 409 `turn_conflict`; no mutation |

Pending expiry is 24 hours from first reservation, never extended by retries;
expiry is checked again before committing a result or acceptance, including a
provider call that finishes after its deadline. Cleanup executes on requests/recovery
and leaves no replayable content. At expiry,
freeze the same `abandon` receipt that an explicit abandon would have produced.
An abandon retry after expiry returns it; generation/accept retry returns
410 `turn_expired`. Explicitly abandoned turns return 409 `turn_abandoned` to
late generation/accept. An accepted turn cannot be abandoned or edited through this
API. Unknown or foreign turn/response lookups return indistinguishable 404
`turn_not_found`; a foreign session returns 404 `session_not_found`.

Application acceptance is independent of delivery: after acceptance, known or
ambiguous channel delivery failure does not retract history. A consumer must not
assume the user saw that text. Abandonment only resolves pending turns and is not
a selective erasure API. Failed/incomplete standard streams are never promoted;
existing historical drafts are never migrated into accepted turns. Crash windows
can repeat an upstream call; neither provider execution nor Telegram delivery is
exactly once. Saved explicit generation retries may return their original draft,
but replay uses only the finalized replacement.

## Replay, budgets and lifecycle

Read the latest eligible complete exchanges at or below the snapshot, then assemble
chronologically. Never reinsert replayed text as new inbound rows. Reserve current
instructions, current input/facts, retrieved sections and output first within the
model's whole context bound. Fit complete newest exchanges; omit oldest whole
exchanges until they fit. If even the newest exchange cannot fit, omit history;
if mandatory current content/output cannot fit, fail 400 `context_budget_exceeded`.
No clipped or partial turns, and no context-window guarantee based only on retrieval
section limits. History is untrusted past dialogue, never current facts/instructions.

When enabled and advertised, `adaptive` may omit recent history for a sufficiently
supported standalone request. CH6 retains recent history for older-reference signals;
bounded same-session older retrieval remains deferred. Classifier uncertainty, refusal, invalid output, timeout or failure
falls back to recent history; authorization, source or deletion failure never does.
Observation mode leaves generation on recent history. Selection confers no new
authority and does not change the independent memory/knowledge controls.

Accepted dialogue/results remain in the session until erasure; this contract adds
no automatic expiry for accepted transcripts. Complete explicit finalization removes
superseded draft content from replay; any retained original result needed for an
identical generation retry stays subject to the same session erasure. Abandoned
turns retain only IDs, sequence, terminal timestamps/status and payload-free receipt;
no text or content hash. Accepted binding hashes/receipts live with the session,
not in an independent transcript archive. Pending content lives at most 24 hours
plus recovery cleanup delay; stopped workers may delay physical cleanup, never
eligibility checks.

Before Decisions, retrieval of history, generation, retries and finalize, validate
subject fences and every session dependency, including requests with no new
`knowledge_refs`. Correction/erasure follows ADR 0006: invalidate and erase the
**entire linked session**, including accepted/local turns, drafts, response copies,
prompt/context packets, request hashes and receipts. Commit a minimal
(subject, session ID, terminal timestamp) session fence before cleanup so delayed
calls cannot recreate it; return 410 `session_deleted`, including after cleanup.
This extends the existing linked-session lifecycle rather than exposing new deletion
routes. Consumers rotate to a new session only after canonical revalidation and
pending erasure resolution, and never replay canceled payloads into it.

Full subject purge removes all these rows/fences under the retained subject fence;
it returns 410 `subject_deleted` on subsequent non-purge calls. Fences win over
retry caches. Independent unlinked mentions, backups, retired indexes, Telegram and
provider retention remain separate. Consumers must suppress calls while their local
erasure is queued; the provider cannot know about a fence not yet transmitted.
No derived transcript summaries/vectors/cross-session copies are introduced.

## Errors and downstream acceptance tests

Errors use the existing OpenAI-style `{error:{message,type,param,code}}` envelope,
with bounded generic messages and no transcript, foreign identifiers or upstream
payloads. In addition to codes above: 400 invalid schema/version/selector;
401 absent/invalid authentication; 403 existing service scope denial; 413 body
limit; 409 stale source/reference (`history_source_conflict`); 503 unavailable
storage/recovery (`history_unavailable`). Auth and deletion checks precede cached
success. Ordinary legacy error behavior stays unchanged. Transient 409/503 can be
retried with the same IDs/body and bounded backoff; conflicts/deletion/expiry cannot.

CH2/CH3 and OCH4 must prove: unchanged SDK supplied-history/tool/SSE operation;
no inferred session; off/default/header conflicts; exact scoped capability/receipts;
concurrent ordering and immutable request snapshots; matching/conflicting retries;
replacement versus draft replay; local source linking; expiry/abandon/restart;
budget omission; missing/foreign IDs; failed streams; purge and selective erasure
races; old-generation retries after fences; privacy commands through consumer
barriers; delivery failure with accepted history; no legacy draft backfill. JSON
schema checks alone cannot prove any storage, ordering, scope or erasure invariant.

The [CH7 release checklist](../conversation-history-release.md) records the recent-only release boundary,
coordinated consumer gates and rollback requirements. See [CH6 adaptive selection](../adaptive-history.md)
for its independent opt-in and evaluated limitations.

## CL2 interpretation extension

With personal-memory retrieval enabled, CL2 may interpret the current request
using bounded canonically accepted same-session dialogue. This resolves natural
follow-ups without people/topic/goal/mode metadata; capture still sees only the
original user input. It cannot use pending, rejected or abandoned drafts as dialogue
context. The managed configuration recipe changes to `managed-recent-cl2-1`;
existing configuration conflict rules continue to protect pinned retries. Managed
Responses receipts include `metadata.context_packet`. First-generation responses
also include selected memory-ID/status headers; cached managed Chat receipts do
not reconstruct those headers. See [CL2 behavior](../conversational-learning.md).
