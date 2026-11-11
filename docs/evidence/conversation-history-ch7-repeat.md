# CH7/OCH5 repeat release — 2026-11-11

Published and deployed to `oria-live-test`: SecondContext 0.2.3, Oria 0.2.4,
unchanged MCP 0.2.1.
Includes CH4/CH5 observation, CH6 conservative adaptive selection and provider-only
consumer deployment settings. No schema or policy change: provider schema 5,
Oria schema 0013, disclosure 2026-11-10.1. Existing valid consent stays valid.

## Release decision

Keep recent capture enabled, observation and adaptive disabled, and external
provider knowledge disabled. [CH6's 24-case paired evaluation](https://github.com/bdobrica/SecondContext/blob/main/docs/evidence/conversation-history-ch6.md) preserved
required context but increased aggregate estimated cost 37.5% and median latency
399.5 ms; publication does not justify production adaptive enablement. Oria still
requests recent. Future consumer opt-in needs a durable mode binding and workload
benefit. No older retrieval is added.

## Local verification

- Provider `make verify`: formatting, vet, unit and mandatory PostgreSQL/Qdrant
  integration passed; 144 top-level mandatory cases, no skips.
- Provider history/Decisions API race checks and Decisions/config/evaluator race
  checks passed. Packaged image/migration/independent adaptive flag/capture rollback/
  purge/dirty-schema checks passed.
- Unmodified OpenAI Python SDK 3.48.0 compatibility passed against synthetic
  upstream HTTP with real disposable PostgreSQL. An initial database-free invocation
  skipped and was rerun with the required database.
- Oria `make test-history`: 27 passed against the candidate provider source.
- Both release-selection suites: 14 passed each; public schema/fixture comparison
  passed. No source schema or generated artifact changed.
- First cross-service and packaging attempts hit the shared host Docker subnet
  limit. Sequential retries passed after task-owned networks were removed. No
  unrelated resource cleanup or host-wide prune was performed.
- Oria root `make verify` passed with the installed tool wrapper
  `UV='python3 /tmp/oria-och2-uv.py'`: Ruff formatting/lint, strict mypy over
  82 source files, 14 release checks, 611 unit (two existing upstream warnings),
  219 integration, 392 contract and 13 end-to-end tests.

## Publication

Provider `98328e1139e76ed93a62543caa537f6a632443eb` passed
[CI and packaging](https://github.com/bdobrica/SecondContext/actions/runs/43084625417)
and [publication](https://github.com/bdobrica/SecondContext/actions/runs/43084625302).
Oria `167f7c6296ab04dbb943e74a85c303421ce81f8e` passed
[aggregate and cross-service CI](https://github.com/bdobrica/OriaEngine/actions/runs/43085357569)
and [publication](https://github.com/bdobrica/OriaEngine/actions/runs/43085357530).
Oria CI pins the exact provider release revision. Each selected index contains
linux/amd64 and linux/arm64 images, with verified source/version/revision labels
on both configurations. Host images were pulled and checked before maintenance.
The unchanged MCP 0.2.1 remains at its previously verified release revision.
[Manifest evidence](conversation-history-ch7-repeat-images.json) records index and
platform digests, versions and source commits.

| Component | Published immutable image |
| --- | --- |
| SecondContext 0.2.3 | `quay.io/bdobrica/secondcontext-gateway@sha256:14c686aae83288469788ca41d2f523d69c8ebe9096388a0c2c9ba72eb2d74127` |
| Oria 0.2.4 | `quay.io/bdobrica/oria-engine@sha256:5a58bef3c465b962d13097172649481820ed1f253893fca3cac0d22a5c123037` |
| Astrology MCP 0.2.1 | `quay.io/bdobrica/oria-astrology-mcp@sha256:00953d1f67836dde5386ccca483d47b7c6007e08a77439956f429286e637abb4` |

## Live verification

Only `oria-live-test` was maintained. Gateway/worker were stopped and owning Oria
queries confirmed no unresolved ingress or history work before the provider stopped.
Separate owning-role database dumps, a cold Qdrant archive and private env/key backup
were created outside the checkout. Archive/dump structures passed inspection; a
backup restore was not exercised. No live volume or unrelated resource was removed.

- Owning migration commands report Oria **0013 (head)** and provider **5, clean**.
  No schema downgrade, identity rotation, key rotation or policy change occurred.
- Released worker `check_linked_release.py --generate` passed actual MCP calculation,
  scoped projection, generation, correction, stale-reference rejection, selective
  erasure and full-purge checks against fresh synthetic subjects.
- Released worker `check_history_release.py --generate` passed actual model recall
  of random codes from local dialogue and accepted replacement text, exact retries,
  linked-session erasure and full-purge delayed-write fences, before and after rollback.
- The synthetic reading-focus probe used fresh calculated compatibility facts after
  a horoscope exchange, asked for plain English, then switched to rainbows and asked
  “Tell me more”. All answers remained on the current topic and fit Telegram's text
  limit. This checks topic continuity, not all astrological/factual claims or actual
  private clarification routing. Cross-service tests cover the latter synthetically.
- A live capture-off drill, with callers stopped, recovered an existing local receipt
  and a pending generated acceptance, rejected new local capture, exercised legacy
  generation, and checked selective/full erasure fences. Re-enabling restored recent
  capabilities and model continuity. No adaptive live enable/rollback was performed;
  its independent switch/rollback remains covered by packaged and HTTP synthetic tests.
- Fresh synthetic subjects were purged; private cleanup journals are empty. No real
  account was deleted or messaged. Exact HTTP receipts establish active-store coverage,
  not erasure from backups, Telegram or upstream provider retention.
- Gateway/worker run the same new image and disclosure **2026-11-10.1**. Valid prior
  acceptance remains valid; older consent remains blocked. Recent capture and linked
  projections are enabled. Observation/adaptive and external provider knowledge are
  disabled; Decisions settings are absent from application containers.
- All four application services are healthy; gateway readiness and unchanged Telegram
  webhook URL/update types passed. Private configuration comparison confirms only
  selected image references and explicit default-off observer/adaptive flags changed.
  Credentials, encryption key, namespaces, model budgets and canonical IDs remain.

## Remaining gates

Private Telegram review remains pending: compatibility after private birth
clarification, plain-English follow-up, unrelated topic and its follow-up, plus
correction/forget and confirmed deletion on a disposable account. Prior policy and
deletion checks passed, but these synthetic probes do not establish the new private
conversation review. No transcript needs to be shared.

Adaptive production enablement still requires useful workload economics and an
explicit consumer opt-in frozen in durable prepared work. Publication alone does
not satisfy those gates; Oria remains on recent.
