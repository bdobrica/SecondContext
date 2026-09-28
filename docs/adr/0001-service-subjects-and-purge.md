# ADR 0001: Scoped service subjects and durable subject purge

Status: Accepted — 2026-09-28

## Context

OriaEngine needs one service credential to serve many internal UUID subjects and
an owning-service API to erase their conversational content. The user explicitly
authorized this extension of SecondContext's authentication boundary. Existing
subject-bound bearer credentials must retain their authority restrictions.

## Decision

Introduce separate, opt-in service credentials, each owning a reserved namespace.
Require a canonical UUID subject within that namespace in a dedicated header.
Resolve that subject once in authentication middleware, before any storage or
retrieval. Existing body identity selectors must match it. Service credentials may
only create responses, ingest memory and purge subjects; they do not gain access
to debug, outcome, memory search/list/extract or metrics endpoints. Ordinary user
credentials remain subject-bound and cannot alias a reserved service namespace.

Add the versioned [subject purge contract](../contracts/service-context-v1.md).
Authenticated subject requests hold a PostgreSQL advisory lock throughout all
local and remote work. Purge first persists a deletion fence, then removes vectors
with synchronous confirmation and cascades canonical user deletion in a transaction
that records completion. Retries are idempotent; partial failure leaves the fence.
A retained external subject identifier and timestamps prevent delayed requests
from recreating data. There is no public reset of that fence; a newly registered
application user gets a new subject UUID.

## Consequences

Migration 000003 and a coordinated rollout are required. All API instances sharing
the storage must run the same fencing implementation with authentication enabled
before offering purge. Offline/direct database/index writers must be quiesced;
they are not authorized through this HTTP boundary. Deletion does not erase backup
snapshots, retired index collections or upstream provider retention.

A dedicated database connection per in-flight authenticated request avoids pool
starvation from advisory-lock waiters while repositories use the normal pool.
Operators must budget for these connections and bound deployment concurrency.
No request body, model output or memory can confer service authority. Stateless
unauthenticated development calls remain supported, but subject purge is never
available without authentication. Existing response/ingest idempotency limitations
remain; this decision does not claim exactly-once conversation generation.
