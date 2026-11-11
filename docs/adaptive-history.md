# Adaptive conversation history

CH6 implements optional `adaptive` selection under [ADR 0007](adr/0007-conversation-history.md)
and the existing [v1 history contract](contracts/conversation-history-v1.md).
It is **disabled by default**. [Evaluation evidence](evidence/conversation-history-ch6.md)
shows preserved synthetic continuity and selective savings, but higher cost/latency
across the full evaluated mix. This is not a recommendation to switch all traffic.
Oria's normal adapter still requests `recent`; changing its durable preparation and
rollout configuration is separate consumer work.

## Enablement and rollback

Set `HISTORY_ADAPTIVE_ENABLED=true` only in SecondContext. Published Compose uses
`SECOND_CONTEXT_HISTORY_ADAPTIVE_ENABLED`. This is independent of
`DECISIONS_OBSERVE_ENABLED`: the latter observes `recent` without changing its prompt.
Both use the [provider-owned Decisions settings](decisions-observation.md), including
an explicit key and separate cache partition secret. Either switch requires valid
credentials; neither turns on managed history, memory, or external knowledge.

With ready managed history, the adaptive switch adds `adaptive` to the existing
capability list. Clients must explicitly request that mode. Ordinary OpenAI clients
continue to supply their own dialogue unchanged; legacy Responses and managed `off`
never classify. The wire schema, endpoint/subject authority and generation bodies
are unchanged. No migration or new data store is required (schema 5).

Enable only after reviewing the workload's paired quality and cost/latency evidence,
verifying provider access, publishing a compatible image and completing consumer
consent/capability gates. A flag is operator configuration, not proof of those gates.
No production flags or image references are changed by this implementation.

To roll back, direct **new** requests to `recent`, then disable
`HISTORY_ADAPTIVE_ENABLED`. Accepted dialogue remains available to recent replay;
finalization, abandonment and deletion remain available. A disabled provider rejects
adaptive generation, including retries, with `unsupported_feature`. Drain pending
adaptive generation first, or abandon it using its original identity; never rewrite
an existing request's history header to recent. Saved generated drafts can still be
finalized after disablement. Existing receipts remain exact. Observation may stay on
or be independently disabled. Keep the current fencing implementation and schema.

## Selection and retry binding

The `standalone-1` recipe omits recent replay only when the validated Decisions
result succeeds and **both** recent-dependence and older-reference probabilities
are at most **0.05**. This is more conservative than the observer's diagnostic
0.2/0.8 thresholds. A reference to older discussion retains recent history; it does
not retrieve older turns or claim that missing facts are known. Missing/invalid
answers, refusal, uncertainty, input bounds, timeout, access errors and outages all
retain the budget-fitted recent baseline. A prompt with no fitting history skips
the classifier entirely. Classifier savings are not assumed for short requests.

Current instructions, prepared input, freshly supplied facts, retrieval sections
and output reservation always remain. Whole exchanges are fitted first, and only
those fitting exchanges are labeled recent in the classifier. History remains
untrusted dialogue; decisions change no authority or deterministic memory ranking.
The same subject lock and canonical/source/deletion checks run before evidence
hydration, immediately before classification, after classification and before the
selection is committed. Canonical/storage/authorization failures abort, even if
classification confidently says standalone. A remote cache is never authority.

An adaptive reservation pins its request/configuration, sequence snapshot and
context generation first. Existing nullable `selected_turns` is NULL only while
its selection is unresolved. Candidates are canonical accepted rows at or below
that snapshot. Before the first generator call, commit the selected fitting turn
IDs once; an empty array is a resolved empty selection. A generation failure or
restart reuses that array and never classifies again. If the process dies before
selection commits, it may reclassify the same immutable snapshot; no generator
attempt has occurred. Existing recent/off reservations keep their previous bindings.

The adaptive configuration hash additionally binds the classifier model/recipe,
endpoint, limits/deadline, segment width and selection recipe/threshold. Flags and
credentials are excluded so rotation does not reinterpret a request. Drift fails
resumption; never silently change a pending turn's selection. Abandon/expiry wipes
the selection through existing cleanup, and session/subject erasure deletes it with
its source turns. No summary, transcript embedding, cross-session replay or automatic
transcript-to-memory extraction is introduced. Older retrieval remains deferred.

## Measurement and evaluation

Decisions metrics add bounded `selection=observe|recent|standalone|pinned` labels to
separate diagnostic suggestions from applied routing. `selection_reused` and
`no_history` outcomes record generation attempts without a new classifier call;
they do not invent classifier usage/cost. Generation retries are measured separately
from the original attempt. Saved-result retries make no provider call. Costs with
missing usage/rates stay unknown, and priced coverage must be checked.

The evaluator compares independent recent/adaptive generations on 24 authored cases:
12 calibration and 12 holdout, including exact references, corrections, prepared
reading handoff, a topic switch followed by a reference, missing older facts, and
long history. It reports threshold sweeps separately by split, missed required
context, exact fact/topic assertions, classifier and paired generation usage/cost,
and latency. The fixed conservative threshold is not selected by holdout results. The limited
calibration sweep is an initial check, not proof of calibrated probabilities.

```sh
go run ./cmd/historyeval -live -generate -adaptive > /private/evidence/adaptive.jsonl
```

This makes one access probe, 24 Decisions calls and 48 bounded Chat Completions.
The access probe has a five-second deadline; corpus calls use `DECISIONS_TIMEOUT`
(default two seconds), as runtime does. The access probe may fail before any corpus
calls. No production transcript or database is read. Default output is numeric.
Optionally supply `-review-output /private/new-file.jsonl` to retain synthetic answers
for review; the file is created exclusively with mode 0600. Never point it at a
customer transcript. The package's `/app/historyeval` supports the same flags.

Exact checks catch wrong names, choices, corrections and fabricated older facts;
they do not establish all aspects of conversation quality. Review answers as well.
This small authored holdout is an initial check, not population-level calibration.
Production enablement still needs workload evidence and monitoring; useful long
standalone savings do not imply aggregate savings or a latency improvement.
