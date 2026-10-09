# ADR 0005: Documentary candidates with transactional retraction

Status: Accepted — recorded retrospectively 2026-10-10

## Context

Useful grounded retrieval is already available. Consumers may also want candidate
entities, people, topics, claims and relationships from documents. Extraction must
preserve source evidence and permit retraction without silently mutating a
consumer's cognitive model or presenting source claims as observations.

## Decision

Keep extraction disabled by default and require an explicit authenticated request
for a ready source or selected documents. Use separately configured model
credentials and bounded synchronous model IO. Ingestion, search and gateway
response generation never invoke extraction.

Validate a strict five-kind schema and require a nonblank exact quote from a
selected canonical chunk. Store deterministic candidate IDs, canonical IDs,
retained evidence snapshots and extraction recipe/model metadata. Label every row
`documentary_claim` and `consumer_decides`; consumers explicitly own promotion.
Contradictory sources remain independent assertions without identity merging or
automatic truth arbitration.

Release the initial read transaction during model IO, then lock and revalidate
the selected canonical snapshot before committing. Evidence changes return
`409 evidence_changed` with no partial output. Successful recomputation replaces
active candidates only for selected documents; failed extraction retains prior
results. Repeated identical recipe/chunk/assertions reuse their IDs.

Use Postgres triggers to retract candidates atomically with document/chunk/source
changes and deletion, including direct SQL and cascading writes. Candidate audit
rows intentionally have no cascading evidence foreign keys, so their snapshots
survive deletion. Expose owner-scoped active/retracted/all polling and explicit
idempotent candidate purge. Consumers reconcile complete paginated snapshots and
remove projections for retracted or purged IDs.

## Consequences

Reviewers can trace old assertions after source deletion, and rollback preserves
retraction consistency. Retained snapshots are additional private source content;
deleting a source or purging a SecondContext subject does not erase these audits.
Complete erasure requires candidate purge and separate backup retention handling.

Exact-quote membership does not prove entailment or model accuracy. Every explicit
extraction call may incur model cost. Offset polling is not a transaction snapshot
or exactly-once event stream. There is no automatic audit expiry, durable extraction
job history, webhook, promotion adapter or background recomputation in this MVP.

See [candidate behavior](../knowledge-base.md#optional-derived-knowledge),
[API and polling details](../../services/knowledge-bootstrap/README.md#optional-derived-knowledge-bridge-k10)
and [audit erasure](../operations.md#deletion-and-candidate-audit-retention).
