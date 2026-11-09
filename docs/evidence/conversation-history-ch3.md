# CH3 verification — 2026-11-09

Implemented on the CH2 baseline `69d491a`: migration 000005, separate durable managed
turns, RFC 8785 request bindings, recent whole-exchange replay, scoped capability and
finalize endpoints, explicit accepted replacements, local exchanges, pending barriers,
expiry/abandonment, and linked-session terminal fences. No dependency was added.
Public JSON Schema/fixtures are unchanged from CH1; no accepted ADR was rewritten.

## Executed checks

- Focused API/config/repository/canonicalization unit tests passed. Canonicalization
  covers RFC 8785 number boundaries, UTF-16 property ordering, duplicate names,
  invalid surrogate pairs and non-finite numbers. Header/input/finalize validation,
  shared finalize fixtures, Unicode bounds, request binding and whole-turn budget
  omission passed.
- Root `make verify` passed: formatting, vet, unit tests and mandatory real
  Postgres/Qdrant integration, with **no integration skips**. Migration 000005
  applied cleanly and repeated migration was a no-op. Pinned Postgres 16.13-alpine
  and Qdrant v1.15.5 ran only in the disposable
  `oria-ch3-secondcontext-integration` project (test ports 55462/56363).
- Eight managed-history integration cases passed: acceptance/result retries,
  replacement-only replay, eight-exchange chronological windows, legitimate repeated
  text, no legacy-draft backfill, no supplied-chat archival, separate sessions,
  atomic source linking for local acceptance and generation reservation, concurrent same/different turns, restart/provider
  failure recovery, terminated lock-connection recovery, configuration drift,
  expiry before result/acceptance, payload-free abandonment, capture disablement
  with finalize recovery, scope/foreign IDs, and Unicode NUL preservation.
- A real HTTP synthetic provider verified managed Chat output limits, durable
  gateway response IDs, direct source-reference linking, incomplete `length`
  rejection and no provider call on cached-result retries or erased sessions.
- Deliberately failed SQL cleanup proved session fences commit before deletion
  cleanup: empty-ref/off requests, old generation retries, local turns, finalization
  and legacy Responses fail while cleanup is pending and after it finishes.
  Correction, stale revisions and whole-subject purge coverage passed. Existing
  linked-projection tests passed unchanged.
- `go test -race -count=1 ./internal/api -run
  'TestManagedHistory.*Integration|TestManagedSelectors|TestManagedInputProfile|TestHistory'`
  passed with the real isolated database, including concurrency/recovery.
- Unmodified OpenAI Python SDK **3.48.0** passed the existing supplied multi-turn,
  separate-chat, function-tool, SSE, usage, structured-output, error and discovery
  checks as part of `make verify` using `/tmp/ch2-sdk/bin/python`.
- Oria `scripts/sync_history_contract.py --source /tmp/ch3-secondcontext --check`
  passed; `tests/contract/test_history_schema.py`: **32 passed**. No Oria runtime
  files changed.
- Published Compose fragment validated with `docker compose ... config --quiet`
  and synthetic configuration. Final diff and explicit changed-file hashes were
  reviewed; generated caches and local PLAN execution records are not committed.

Tests ran against a staged working copy under `/tmp/ch3-secondcontext` because the
SecondContext checkout is outside the writable workspace. Installation uses guarded
preimage/content hashes so the reviewed and tested files are the installed files.
The implementation commit excludes `PLAN.md` under its existing local convention.

## Boundaries

No production deployment, live migration, image publication, live model generation,
provider quality/cache measurement, Decisions integration, Telegram flow or Oria
full verification was performed. The container-image build gate `make test-docker`
was not run; its expected migration version was updated to 5. No destructive down
migration was executed: dropping history/session fences is intentionally refused.
Rollback is capture disablement on compatible code/schema, not an older CH2 image.

The initial eight-exchange window is verified structurally with synthetic dialogue;
model-quality evaluation remains OCH4/CH7. Token accounting conservatively bounds the
serialized request; operators must choose model-safe ceilings and verify provider
support. New capture is **disabled by default**. Oria OCH2/OCH3, disclosure acceptance
and coordinated release remain prerequisites for application enablement. External
`KNOWLEDGE_ENABLED` remains disabled in the deployment fragment.
