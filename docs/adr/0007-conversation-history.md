# ADR 0007: Optional conversation history and application acceptance

Status: Accepted — 2026-11-09. Contract decision; runtime implementation pending CH2/CH3.

## Context

Persisting messages under a session does not provide multi-turn prompt continuity.
Ordinary OpenAI clients already send their conversation; Oria instead needs the
service to replay accepted replies, including application replacements, with the
same ownership and erasure boundaries as linked context.

## Decision

Adopt [conversation-history v1](../contracts/conversation-history-v1.md).
SecondContext owns ordered transcripts, final prompt assembly and history selection.
Consumers own consent, prepared input, canonical facts, reply acceptance and delivery.
No shared database access or internal implementation imports are introduced.

Supersede only the exhaustive service-route allowlist in ADR 0001 as extended by
ADR 0006: additionally permit POST `/v1/chat/completions`,
`/v1/context/capabilities` and `/v1/context/turns/finalize`. Retain every existing
route, namespace restriction, subject lock, identity check and purge fence. No
transcript browsing/editing, model discovery or arbitrary session administration
is granted to service credentials. Ordinary subject-bound clients keep `/v1/models`.

Client-supplied dialogue is the default. Managed replay requires explicit session,
turn identity and history selection; never infer a session from a subject. In
explicit acceptance mode, generated drafts are ineligible until the consumer
finalizes the accepted text. Finalization can instead terminally abandon a pending
turn. Application-origin prepared local exchanges use the same narrow endpoint.
Acceptance does not assert channel delivery. Ordered snapshots and immutable retry
bindings prevent duplicate accepted turns, not duplicate upstream execution.

Extend ADR 0006's whole-linked-session cleanup to all new history content. Retain
only payload-free session/turn fences needed to reject delayed recreation; full
subject purge removes these under the existing subject fence. Validate source
revisions and deletion fences before classification and generation, even when the
new request has no source references. Old retained drafts are never backfilled as
accepted history. Replay does not create semantic memory or new authority.

Start with bounded recent history. Decisions observation/adaptive routing are later
capabilities, preserving recent history on classifier failure, never on scope or
erasure failure. This does not supersede ADR 0002's deterministic memory ranking or
ADR 0004's independent memory/knowledge controls. Cross-session recall, summaries,
transcript embeddings and automatic memory extraction remain deferred.

## Consequences

New schema, recovery logic, service-route tests and consumer changes must precede
capability advertisement. Existing Responses requests remain compatible. Chat
Completions receives a documented supported subset with standard responses/SSE;
unsupported behavior-affecting fields fail explicitly. The contract is not a claim
of full OpenAI parity or already-deployed endpoints. Erasure and finalize recovery
must remain operable when generation/history selection is disabled.
