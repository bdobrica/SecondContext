# CH7 release preparation — 2026-11-10

Prepared on provider `f0dd2682e215fb72520224509eee04cbf14ba805` and Oria `d29d7c7`.
Gateway candidate **0.2.2** coordinates with Oria application/MCP **0.2.1**.
Publication/live rollout remains pending; version metadata is not a registry receipt.
See the [release checklist](../conversation-history-release.md) and the companion
[Oria evidence](https://github.com/bdobrica/OriaEngine/blob/main/docs/evidence/conversation-history-release.md).

## Changes

The owning runtime, public wire schema and accepted ADRs are unchanged. This slice
prepares publication metadata, documents the recent-only boundary, updates the
provider deployment example and expands the built-image gate. Packaging now checks
history tables/capabilities/local acceptance, recreation with capture disabled,
recovered receipt, new-capture rejection, purge and delayed retry fencing, in addition
to existing migration idempotency and dirty-version startup refusal. Disposable
PostgreSQL readiness uses TCP to exclude its bootstrap-only socket server.

The consumer adds independent deployment switches/model budgets, real-gateway
rollback tests and a cleanup-journaled synthetic live smoke command. It uses only
versioned HTTP; no provider database access or internal implementation import.

## Executed verification

- Root `make verify` passed: formatting, vet, short suite and mandatory real
  PostgreSQL/Qdrant integration with no skipped integration cases. The unchanged
  OpenAI Python SDK **3.48.0** passed via `SECOND_CONTEXT_CHAT_SDK_PYTHON`.
- Dependency-backed API history race checks passed (**12 cases**) with
  `go test -race -count=1 -v ./internal/api -run
  'TestManagedHistory.*Integration|TestManagedSelectors|TestManagedInputProfile|TestHistory'`.
- `make test-docker` passed using the actual built runtime image and owner migration
  executable: migration 5 clean, migration rerun, history capability/local capture,
  capture-off receipt recovery, rejected new local turn, full purge and retry fence,
  then deliberately dirty migration blocks API startup. No model calls in this lane.
- Release-matrix unit suite: **14 passed**. Public history schema/fixture comparison
  with Oria passed. No source schema or generated contract drift.
- Oria cross-service gate: **23 passed**, with the real provider executable over
  loopback HTTP and synthetic model/Telegram replies. Coverage includes earlier
  OCH4 scenarios plus both-switch rollback/re-enable, undispatched/lost acceptance,
  pending draft cancellation, delivery recovery, selective/full erasure, and the
  release command's continuity, cleanup journal and cleanup-only recovery.
- Companion Oria aggregate verification is recorded in its evidence after completion.

Tests used a temporary source copy `/tmp/secondcontext-ch7` because the provider
checkout is outside the writable workspace. Reviewed files are installed with
preimage/content hash checks; the preexisting local PLAN remains unstaged. No new
dependency was introduced. Only disposable `oria-ch7-provider-verify`,
`oria-ch7-provider-race`, `oria-ch7-provider-packaging` and random Oria test projects
were created; they were removed with their own storage. No host-wide pruning.

## Remaining operational gates

No release push, Quay publication, live migration, provider generation, private
Telegram exercise or live rollback drill is established by this record. Read-only
GitHub inspection found successful prior CH3 Verify/publication workflows, which
are not publication evidence for candidate 0.2.2. Confirm exact commits, manifests,
labels and digests before replacing live references. Scope any maintenance to
`oria-live-test`; preserve storage, secrets, namespace and webhook.

Schemas remain provider 000005 / Oria 0013. Current consistent disclosure and fresh
consent are required before new capture. The provider and Oria default off. Do not
downgrade to pre-history images or drop session fences. Drain never-dispatched local
acceptances before disabling provider capture; generated draft acceptance and
existing local receipt retries remain operable while disabled. No adaptive rollback
is claimed: CH4–CH6 remain unimplemented and unadvertised, and the first release is
recent-only with external `KNOWLEDGE_ENABLED=false`.
