# CH7 release preparation — 2026-11-10

Prepared on provider `f0dd2682e215fb72520224509eee04cbf14ba805` and Oria `d29d7c7`.
Gateway **0.2.2** is published and deployed in `oria-live-test` with Oria/MCP
**0.2.1**, with recent history enabled. Private Telegram policy/deletion passed; conversation continuity requires correction
and a retest.
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
- Companion Oria aggregate verification passed: formatting/lint, strict types,
  14 release checks, 600 unit, 210 integration, 391 contract and 13 end-to-end tests.
  Its final smoke-command type adjustment passed 3 focused real-gateway tests again.

Tests used a temporary source copy `/tmp/secondcontext-ch7` because the provider
checkout is outside the writable workspace. Reviewed files are installed with
preimage/content hash checks; the preexisting local PLAN remains unstaged. No new
dependency was introduced. Only disposable `oria-ch7-provider-verify`,
`oria-ch7-provider-race`, `oria-ch7-provider-packaging` and random Oria test projects
were created; they were removed with their own storage. No host-wide pruning.

## Publication and live verification

Provider release source `9b0d5b45f5f6a17c6b5fbd3b381116ea96448ce3` passed
[CI/packaging](https://github.com/bdobrica/SecondContext/actions/runs/42763889495)
and [publication](https://github.com/bdobrica/SecondContext/actions/runs/42763889444).
Oria source `6439c6f1ed02d4ef49403f27cbb129a1ead00384` passed its aggregate/replay CI
and publication; exact image digests and all checks are in the companion evidence.
The provider index is
`quay.io/bdobrica/secondcontext-gateway@sha256:180478c974b5529f074187317bf44f0f372f414fae6a03bb43926561ac1f7557`,
with linux/amd64 and linux/arm64 manifests and verified pulled source/version labels.

Only `oria-live-test` was maintained. Owning-role database dumps, cold Qdrant archive
and private configuration/key backups were made before migration; their structure
was checked, but restore was not exercised. The owning migrator reports **5, clean**,
and Oria reports **0013**. Storage, credentials, namespace, encryption key and
webhook were preserved; no host-wide cleanup or destructive schema downgrade.

The released worker adapter's real-provider probes passed linked calculations,
capabilities, local dialogue and accepted replacement recall, identical retries,
correction, selective erasure, full purge and delayed-write fences. Fresh synthetic
subjects were purged; no real Telegram user data or messages were used. A live
capture-off drill recovered a pending generated acceptance and an existing local
receipt, rejected new local capture, generated through the legacy path, and erased
sessions/subjects while disabled. Re-enabling restored recent-only capabilities.
Oria callers remained quiesced during that drill; actual Telegram delivery is not
claimed by it. Consumer delivery recovery is covered by the synthetic replay gate.

Oria gateway/worker now use disclosure **2026-11-10.1** and require fresh acceptance.
Recent capture/linked projections are enabled; external `KNOWLEDGE_ENABLED` remains
false. Services/readiness are healthy; existing Telegram webhook registration was
verified unchanged, with zero pending updates at inspection.

## Remaining review

The participating user confirmed policy acceptance and account deletion. Conversation
review failed: a first reading was absent from follow-up history and a later request
was terminally rejected by the conservative prompt budget. Oria 0.2.2 prepares
committed profile projections before the first reading and sets its bundled provider
input budget to 65,536 with 4,096 output tokens reserved. The provider default and
runtime are unchanged. Oria’s regression and aggregate tests passed; see companion
evidence for publication, rollout and the required private conversation retest.
Do not infer them from HTTP probes. No private payloads, credentials or cleanup
journals belong in evidence. Active-store erasure receipts are not claims about
backups, Telegram or upstream retention. CH4–CH6 remain unimplemented/unadvertised:
no Decisions/cache-economics or adaptive-to-recent rollback is claimed.

## Consumer correction rollout

Oria patch `cedda0082470ba0e24a9a549e8deeaa7f93ee5bc` is published/deployed as
application 0.2.2; MCP remains 0.2.1 and this provider retains its existing 0.2.2
image/schema. The bundled provider input budget is now 65,536 conservative units.
The operator verified no unresolved Oria turns before changing the configuration
binding. Fresh backups were structurally checked; no restore was exercised.

Oria aggregate verification passed 600 unit, 212 integration, 391 contract and
13 end-to-end tests; 24 real-gateway tests passed, plus the strengthened profile-edit
case. CI/publication and the deployed immutable digest are in the companion evidence.
Live-model probes passed full calculated natal/transit readings, accepted-history
follow-up recall, another daily reading and an explicit date, in addition to the
existing replacement/erasure smoke test. Synthetic subjects were purged; no Telegram
messages or real account payloads were used. Services and ingress are ready. Private
Telegram continuity retest remains pending, including the snapshot-only symptom.
