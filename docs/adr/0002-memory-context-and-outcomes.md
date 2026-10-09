# ADR 0002: Canonical memory, cognitive context and recoverable outcomes

Status: Accepted — recorded retrospectively 2026-10-10

## Context

The implemented MVP needs to reuse prior interactions, explain selected context,
generate communication strategies and learn from reported outcomes. Small,
inspectable heuristics are sufficient; a trained predictive model is outside the
MVP. Model output and cross-store operations can fail independently.

## Decision

Keep canonical interaction and cognitive state in Postgres and use Qdrant as a
dense/sparse memory index. Validate/repair structured extraction before storing
memory and its entities. Track person estimates per topic and beliefs with
confidence and evidence memory IDs; retain contradictions rather than presenting
model estimates as settled facts.

Retrieve with owner/expiry/confidence/type/people/topic filters, then apply
configurable salience weights, recency decay, goal overlap and summary redundancy
suppression. Use deterministic Go heuristics instead of introducing a learned
reranker. Persist the bounded context packet and structured scenario plan with
assistant messages. Expose development-only inspection and person-model editing.

Make outcome processing recoverable through a tenant-scoped idempotency key,
request hash and durable stage status. Reserve the outcome transactionally before
memory/index/model/belief/graph work. Use stable outcome-memory, vector-point and
graph identities. Replaying the same request resumes incomplete stages;
conflicting payload reuse returns 409. Persist failures rather than deleting
canonical work to compensate for remote errors.

Require visible dependency-backed integration checks alongside formatting, vet
and fast unit tests. Evaluate response usefulness with baseline comparisons,
labeled retrieval cases, optional model judging and explicit manual ratings.

## Consequences

Operators can inspect selected evidence and retry outcome failures. Postgres
backups must retain stage/idempotency state and deletion fences. The gateway has
no general-purpose memory-index rebuild CLI; a canonical database restore and a
Qdrant snapshot need an explicit reconciliation plan.

Salience and scenario success estimates are heuristics. The feedback loop is not
a trained predictor or a general contradiction solver. Outcome recovery does not
make response generation or every ingest exactly once. Debug routes are absent
outside development-like environments. Broader editing, archival/consolidation
and automated expiry remain deferred.

See [architecture](../architecture.md), [operations](../operations.md),
[demo](../demo.md) and [evaluation](../evaluation.md).
