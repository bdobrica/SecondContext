# CH7/OCH5 repeat release — 2026-11-11

Candidate: SecondContext 0.2.3, Oria 0.2.4, unchanged MCP 0.2.1.
Includes CH4/CH5 observation, CH6 conservative adaptive selection and provider-only
consumer deployment settings. No schema or policy change: provider schema 5,
Oria schema 0013, disclosure 2026-11-10.1. Existing valid consent stays valid.

## Release decision

Keep recent capture enabled, observation and adaptive disabled, and external
provider knowledge disabled. CH6's 24-case paired synthetic evaluation preserved
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

## Gates

Oria aggregate verification, publication and live rollout are pending. Pin Oria cross-service CI
to the exact published provider revision. Inspect both image platforms and OCI
labels, back up owning stores, drain pending work, and use only `oria-live-test`.
Keep the existing credentials, identities, webhook, volumes and erasure recovery.
Use fresh synthetic subjects for live generation/continuity, topic switching and
rollback/erasure checks. Health alone is insufficient.

Private Telegram review remains separate and pending: compatibility after private
birth clarification, plain-English follow-up, unrelated topic and its follow-up.
Prior policy acceptance/deletion passed; synthetic tests cannot close this gate.
