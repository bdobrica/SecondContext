# ADR 0006: Scoped linked context projections

Status: Accepted — 2026-11-08

Supersedes only ADR 0001's exhaustive service-route allowlist: add the four explicit
POST knowledge upsert/query/delete/capabilities routes. Existing subject selection,
namespace restrictions, authentication, subject locks and purge fences remain.
No other service routes or tool/account authority are granted.

Oria requests linked salience and interpretation continuity while owning canonical
private events and entities. Add [linked-context v1](../contracts/linked-context-v1.md)
as an independent Postgres-indexed projection lane. Keep canonical inputs in Oria;
SecondContext receives minimal summaries, stable revisioned references and typed
salience/themes. No new projection content enters the legacy Qdrant/cognitive/graph
pipeline. This bounds lifecycle ownership without an untracked derivative store.

Authenticate every projection request and serialize against subject purge. Upserts
are revision-aware and operations have immutable hashes. Erasure commits a terminal
record fence and payload-free pending reservation before transactional cleanup;
queries and generation suppress stale/fenced dependencies during failures. Full
subject purge cascades all projection tables and retains its existing subject fence.

An additive `knowledge_refs` response field selects references already checked by
the consumer. Verify them again and persist source/session links before generation.
Correction invalidates dependent summaries and linked sessions. Selective erasure
removes entire linked sessions, including all user messages, assistant drafts and
context packets, plus dependent projections and their indexes/links. Independent
human mentions and external retention remain outside this coverage. Complete
receipts require completed cleanup; failed SQL work remains resumable.

Use bounded deterministic salience and traditional topic tags as uncertain metadata,
never chart facts or authority. The linked lane needs no embedding/model dependency
for mutation or retrieval. Migration 000004 and a coordinated release are required;
existing response/ingest/purge clients remain compatible. Consumers must not infer
capability from health or deploy ingestion before lifecycle verification.
