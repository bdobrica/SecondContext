# Service context v1 verification — 2026-09-28

Implemented the user-authorized OriaEngine integration: opt-in namespace service
credentials, authenticated subject fencing and retryable purge, migration 000003,
and a versioned wire contract/accepted ADR. Existing subject-bound credentials
remain subject-bound. No dependency additions or live-provider credentials.

## Executed checks

- Focused short-mode Go tests for config, auth, API, repositories and Qdrant.
- Real PostgreSQL 16.13 and Qdrant 1.15.5 in unique temporary Compose projects with
  dynamically assigned host ports. The new integration tests use synthetic LLM
  responses and real memory embedding vectors/retrieval (no OpenAI calls).
- `TestSubjectPurgeRecoveryIsolationAndContinuity`: prior preference retrieval,
  two-user isolation, foreign session denial, all canonical subject table families,
  orphan vector removal, retained foreign vectors, remote failure/retry, a simulated
  SQL deletion failure after remote success, API-object restart, repeated purge,
  absent subjects, deletion-fence enforcement and ordinary-token compatibility.
- `TestPurgeWaitsForInflightRequestAcrossServers`: separate API instances share the
  PostgreSQL fence across an in-flight LLM call and purge; late writes receive 410.
- Full **`make verify` passed**: gofmt check, `go vet ./...`, short unit lane, and
  mandatory verbose integration lane with no skipped tests. Existing tenant-isolation
  and outcome recovery tests ran. The runner received only its isolated database DSN.
- After the final gate, migration down one step reported `version=2 dirty=false`;
  upgrade, repeated upgrade and version inspection reported `version=3 dirty=false`.
- Test containers and their volumes/networks were removed after each run.

The first real-Qdrant test exposed a pre-existing collection reuse failure: sanitized
errors no longer contained the text checked by EnsureCollection. Typed 409 conflict
classification fixed the blocker. Index upserts now request `wait=true` and require
completed acknowledgement so a subject request cannot finish with a queued index
write that races later purge. The synthetic Qdrant fixtures now reflect that contract.
The focused and full suites passed after these changes.

## Limits and repository hygiene

No live OpenAI/Telegram calls, operator database migrations, real-user purge,
production rollout, remote publish or secret configuration changes were performed.
The consumer adapter is separately tested in OriaEngine; these Go tests exercise
SecondContext's actual handlers/stores with synthetic LLM responses.

Purge covers active canonical storage and the configured index collection. The
external subject/timestamp fence remains intentionally; backups, retired indexes
and provider retention have separate lifecycles. All API writers must use the new
authenticated fence before purge is enabled. Dedicated subject-lock connections
require deployment connection budgeting. See the ADR and operations documentation.

The pre-existing untracked CODE_REVIEW.md is untouched. PLAN.md's follow-up entry
remains outside the implementation commit under that repository's internal-plan
convention. Public decisions, contracts, setup and verification are committed here.
