# Linked context v1 verification

Date: 2026-11-08. Source feature, not a published image or live-provider check.
[ADR 0006](../adr/0006-linked-context-projections.md) and the
[wire contract](../contracts/linked-context-v1.md) define the implemented lane.

- `make fmt-check vet test-unit` passed with the installed Go toolchain.
- The mandatory dependency-backed lane passed via
  `bash scripts/test-integration.sh`, with isolated Oria test PostgreSQL/Qdrant,
  migrations and no skipped integration cases. The root `make verify` invokes
  that existing non-executable script directly and encounters permission denied;
  its component gates were therefore run explicitly. No unrelated Makefile/file-mode
  change is bundled into this feature.
- New real-HTTP handler tests cover same-subject idempotency, conflicting bodies,
  owner isolation, unchanged service route restrictions, exact links in generation,
  pending deletion fences after a forced SQL failure, process restart/retry,
  linked-session/draft/context erasure, terminal unknown-record fences, denied
  resurrection, full subject purge and delayed subject writes.
- Additional tests cover source-revision invalidation after correction, clearing
  linked sessions, rejection of stale source writes, harmless cached acknowledgements,
  and latest user-stated significance overriding inferred relevance.
- Oria's actual Python HTTP adapter was exercised against the built gateway binary
  with isolated PostgreSQL/Qdrant: authenticated capabilities, duplicate upsert,
  typed query, duplicate selective delete, denied high-revision resurrection and
  full subject purge all passed. No mocks were used at that HTTP/storage boundary.

The legacy response, memory, retrieval, service-auth and subject-purge suites also
passed. New projection writes do not call embedding/chat providers or legacy graph
writers. Generation tests use a fake LLM, and the real binary lifecycle check does
not request generation. No OpenAI, Telegram, live MVP or other project was accessed.
Publication, image rollout and production data-use enablement remain separate work.
