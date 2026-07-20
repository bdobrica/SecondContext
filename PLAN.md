# SecondContext Review Remediation Plan

Created: 2026-07-20
Plan status: Active
Source of work: `CODE_REVIEW.md`

## Objective

Resolve the code-review findings in dependency order while preserving existing user work, adding regression coverage, keeping public documentation accurate, and creating one narrowly scoped implementation commit per successful step.

`PLAN.md` and `CODE_REVIEW.md` are internal execution records. Their updates must remain outside implementation commits.

## Current worktree baseline

The user-owned observability/configuration work present during the review was committed before remediation began:

- commit: `a3f0509` (`feat(observability): add API logs and metrics`);
- internal records remaining outside the commit: `PLAN.md` and `CODE_REVIEW.md`.

Several planned steps touch files changed by that baseline commit. Inspect the live code and diff first and preserve its observability behavior while implementing remediation.

## Operating rules

Apply these rules to every step below.

1. Start only when all dependencies listed for the step are complete.
2. Re-read the relevant finding in `CODE_REVIEW.md` and inspect the current code before editing; line numbers may have moved.
3. Set the step to `In progress` in this file before code changes. This status-only edit remains unstaged.
4. Keep each step narrowly scoped. Do not combine unrelated cleanup or formatting.
5. Preserve user changes already present in the worktree. If a planned file has overlapping edits, understand and retain their intent.
6. Implement tests with the change. A step is not successful until every acceptance check passes.
7. After implementation and verification, update all affected public documentation in the same implementation commit. At minimum, inspect:
   - `README.md`;
   - `TODO.md`;
   - `.env.example` for configuration changes;
   - relevant files under `docs/`;
   - `deploy/README.md`, `docker-compose.yml`, and `Makefile` for deployment or workflow changes.
8. Create one implementation commit for the successful step. Stage files explicitly; never use `git add .` or `git add -A`.
9. Before committing, inspect both the staged content and file list:

   ```bash
   git diff --cached
   git diff --cached --name-only
   git diff --cached --name-only | grep -E '(^|/)(PLAN|CODE_REVIEW)\.md$' && exit 1 || true
   ```

10. `PLAN.md` and `CODE_REVIEW.md` remain modified or untracked in the local worktree. Never stage, commit, amend, stash, or otherwise publish their content as part of this plan.
11. Commit only after the staged diff contains the intended code, tests, and public documentation and all acceptance checks pass.
12. After committing, obtain the immutable hash with `git rev-parse HEAD`. Then, without amending the commit:
    - change the plan step from `In progress` to `Completed`;
    - record completion date, commit hash, tests run, and a short implementation note in its execution record;
    - mark each corresponding review finding `Resolved` in `CODE_REVIEW.md`, without deleting its history;
    - if implementation reveals a new issue, add it to `CODE_REVIEW.md` and schedule it here or in `TODO.md`.
13. If checks fail, do not commit. Keep the step `In progress` while actively diagnosing it. Mark it `Blocked` only when it cannot continue, and record the concrete reason and diagnostic evidence.
14. A step is `Completed` only when acceptance checks pass, public documentation is accurate, an implementation commit exists, and the post-commit internal record updates are present locally.

### Commit convention

Use a concise conventional subject where practical:

```text
fix(scope): outcome
test(scope): outcome
feat(scope): outcome
docs(scope): outcome
ci(scope): outcome
```

Each implementation commit should contain code, tests, and affected public documentation. Documentation-only follow-up commits should be exceptional.

### Step status values

- `Pending` — not started
- `In progress` — active implementation
- `Blocked` — cannot continue; explanation required
- `Completed` — acceptance checks passed, public docs updated, and implementation commit created

### Standard verification

Use an isolated writable Go cache if the environment's default cache is read-only:

```bash
GOCACHE=/tmp/secondcontext-go-cache go test ./...
GOCACHE=/tmp/secondcontext-go-cache go vet ./...
```

Run `gofmt` on every changed Go file before tests. Use narrower package tests during development, then run the step's full acceptance suite.

## Dependency order

```text
Step 1 (identity authority)
  ├── Step 2 (token/config fail-closed)
  │     └── Step 3 (trusted proxies)
  └── Step 4 (resource bounds)

Step 5 (outcome consistency) depends on Steps 1 and 4
Step 6 (mandatory integration lane) depends on Steps 1-5
Step 7 (final verification) depends on Steps 1-6
```

Step 2 combines SC-002 and SC-003 because both change authentication configuration parsing in `internal/config/config.go` and must establish one fail-closed startup contract. The acceptance checks still track both findings independently.

## Implementation steps

### Step 1 — Establish one authoritative authenticated request identity

- Status: `Completed`
- Findings: SC-001
- Dependencies: None
- Suggested commit: `fix(auth): enforce tenant identity before context retrieval`

Implementation:

1. Add a request-identity or resolved-metadata value that is computed once after authentication and before handlers perform data access.
2. Define the conflict policy for authenticated requests. Prefer rejecting conflicting `user` and `metadata.user_external_id` selectors with a stable 4xx error; alternatively ignore them consistently if OpenAI client compatibility requires it. Document the selected behavior.
3. Refactor `/v1/responses` so context construction and persistence receive the same resolved identity. Do not let `buildBaseContextPacket` independently prioritize raw selectors.
4. Audit every handler that accepts `user`, `user_external_id`, session IDs, message IDs, memory IDs, person IDs, or topic IDs and route it through the same authority rule.
5. Add a database-backed regression test that:
   - seeds tenant B with a uniquely identifiable memory/person/belief;
   - authenticates as tenant A;
   - submits both `metadata.user_external_id=tenant-b` and `user=tenant-b` variants;
   - proves tenant B data is absent from the retrieval request, upstream prompt, response text fixture, and `metadata.context_packet`;
   - proves the request does not create or alter tenant B state.
6. Add unit tests for identity conflict behavior that do not require Postgres.
7. Update README authentication/isolation behavior and relevant docs.

Acceptance checks:

```bash
GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api -run 'TestAuthenticated|Test.*Tenant|Test.*Isolation|Test.*RequestUser'
GOCACHE=/tmp/secondcontext-go-cache go test ./...
GOCACHE=/tmp/secondcontext-go-cache go vet ./...
```

Also run the new database-backed test with the integration environment from Step 6 if one already exists locally. Until then, record whether it ran or skipped.

Execution record:

- Completion date: 2026-07-20
- Commit: `44f16f07b1c11312c106cb89c08016ff70dbfb63`
- Tests:
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api -run 'TestAuthenticated|Test.*Tenant|Test.*Isolation|Test.*RequestUser'` — passed; database-backed cases skipped because `POSTGRES_DSN` is unset.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./...` — passed; database-backed cases skipped because `POSTGRES_DSN` is unset.
  - `GOCACHE=/tmp/secondcontext-go-cache go vet ./...` — passed.
  - `git diff --check` and staged internal-file exclusion check — passed.
- Implementation note: Added one authenticated user-selector resolver with a documented HTTP 400 `identity_conflict` policy, applied it across response, memory, outcome, and debug endpoints, and made response retrieval and persistence consume the same resolved metadata. Added unit coverage for all selector entry points plus a Postgres regression that seeds and snapshots tenant B memory/person/topic/belief state; the integration test is ready but did not execute locally because `POSTGRES_DSN` is unset.

### Step 2 — Make authentication configuration fail closed

- Status: `Completed`
- Findings: SC-002, SC-003
- Dependencies: Step 1
- Suggested commit: `fix(config): reject ambiguous authentication settings`

Implementation:

1. Replace `getEnvBool` with error-returning parsing and update `Load` to propagate errors for every boolean setting.
2. Reject non-empty invalid values for `AUTH_ENABLED`, `POSTGRES_ENABLED`, `HTTP_METRICS_ENABLED`, and any future boolean option.
3. Require a non-empty subject for every bearer-token entry when authentication is enabled. Remove acceptance of bare tokens unless an explicit compatibility mode is designed, documented, and proven safe.
4. Add a middleware invariant: a successful authentication result must contain a non-empty normalized subject.
5. Validate duplicate subjects and duplicate token values. Reject ambiguous mappings rather than relying on first-match order.
6. Avoid logging secret token values in configuration errors.
7. Add table-driven configuration tests covering unset values, all accepted `strconv.ParseBool` forms chosen for support, invalid values, blank subjects, bare tokens, duplicate subjects, duplicate token values, and valid multi-user configuration.
8. Add an API test proving no accepted token can produce an empty authenticated subject.
9. Update `.env.example`, README, and deployment documentation with the exact token grammar and fail-fast behavior.

Acceptance checks:

```bash
GOCACHE=/tmp/secondcontext-go-cache go test ./internal/config ./internal/api -run 'Test.*(Config|Auth|Token|Bool|Subject)'
GOCACHE=/tmp/secondcontext-go-cache go test ./...
GOCACHE=/tmp/secondcontext-go-cache go vet ./...
```

Execution record:

- Completion date: 2026-07-20
- Commit: `4cbb0c3c28cdad3a2b19dc29fa69c95b1902b837`
- Tests:
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/config ./internal/api -run 'Test.*(Config|Auth|Token|Bool|Subject)'` — passed.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./...` — passed; the first sandboxed run was blocked when an existing `httptest` case could not open a loopback socket, then passed with loopback access enabled.
  - `GOCACHE=/tmp/secondcontext-go-cache go vet ./...` — passed.
  - `git diff --check`, staged diff review, and staged internal-file exclusion check — passed.
- Implementation note: Replaced permissive boolean fallbacks with startup errors for every boolean setting; made bearer-token parsing require unique, non-empty `subject=token` entries without disclosing secrets in errors; and added a defensive middleware invariant that refuses subjectless principals. Added table-driven configuration coverage, an API regression, and exact grammar/fail-fast guidance in the environment template, README, and deployment documentation.

### Step 3 — Introduce an explicit trusted-proxy model

- Status: `Completed`
- Findings: SC-004
- Dependencies: Step 2
- Suggested commit: `fix(http): trust forwarded addresses only from configured proxies`

Implementation:

1. Add configuration for trusted proxy CIDRs or an explicit forwarding-header mode. Default to trusting no proxy.
2. Replace unconditional `middleware.RealIP` with middleware that:
   - captures the immediate socket peer;
   - honors configured forwarding headers only when that peer belongs to a trusted proxy;
   - selects the client address from a well-defined hop direction;
   - rejects or safely ignores malformed addresses.
3. Make rate limiting and request logging consume the same resolved client address.
4. Decide and document behavior for multiple proxies, IPv4-mapped IPv6 addresses, and direct Docker port publishing.
5. Add table-driven tests for direct spoofed headers, one trusted proxy, multiple forwarding hops, untrusted proxies, malformed values, IPv4, and IPv6.
6. Add a regression test proving a direct unauthenticated client cannot rotate `X-Forwarded-For` to bypass its limit.
7. Update `.env.example`, README, Docker/deployment docs, and observability documentation.

Acceptance checks:

```bash
GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api -run 'Test.*(RealIP|ClientIP|Forwarded|Proxy|RateLimit|RequestLogging)'
GOCACHE=/tmp/secondcontext-go-cache go test ./...
GOCACHE=/tmp/secondcontext-go-cache go vet ./...
```

Execution record:

- Completion date: 2026-07-20
- Commit: `62fc90b8ee81a10885cb6796751d4830c9d7f6b5`
- Tests:
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/config ./internal/api -run 'Test.*(Config|RealIP|ClientIP|Forwarded|Proxy|RateLimit|RequestLogging)'` — passed.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api -run 'Test.*(RealIP|ClientIP|Forwarded|Proxy|RateLimit|RequestLogging)'` — passed.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./...` — passed; the first sandboxed run was blocked when an existing `httptest` case could not open a loopback socket, then passed with loopback access enabled.
  - `GOCACHE=/tmp/secondcontext-go-cache go vet ./...` — passed.
  - `git diff --check`, staged diff review, and staged internal-file exclusion check — passed.
- Implementation note: Replaced unconditional forwarding-header trust with a CIDR-configured client-IP resolver that defaults to the socket peer, accepts only `X-Forwarded-For` from an immediate trusted proxy, evaluates multi-proxy chains right to left, falls back safely on malformed input, and normalizes IPv4-mapped IPv6 addresses. Rate limiting and structured request logs now consume the same resolved address. Added configuration, chain, logging, and header-rotation regressions, plus environment, README, Compose, and deployment guidance.

### Step 4 — Bound and strictly validate external input and responses

- Status: `Completed`
- Findings: SC-005
- Dependencies: Step 1
- Suggested commit: `fix(api): enforce request and result limits`

Implementation:

1. Define named, documented limits for:
   - JSON request body bytes;
   - response input text and metadata where needed;
   - list/search page sizes;
   - Qdrant candidate expansion;
   - OpenAI and Qdrant response body bytes.
2. Implement one shared JSON decoder that applies `http.MaxBytesReader`, performs exactly one value decode, verifies EOF, and produces stable errors for malformed, trailing, or oversized bodies.
3. Decide unknown-field behavior per endpoint. Preserve required OpenAI compatibility while using strict decoding on internal endpoints where safe.
4. Enforce maximum list/search limits at the API and service boundaries. Use overflow-safe arithmetic for candidate expansion.
5. Replace unbounded `io.ReadAll` calls with limited reads that distinguish at-limit from over-limit responses.
6. Ensure error bodies do not echo oversized or sensitive upstream payloads.
7. Add boundary tests for empty bodies, exact maximums, one byte/item over, trailing JSON, negative/zero/excessive limits, multiplication overflow, and oversized OpenAI/Qdrant responses.
8. Update README API constraints and any demo/evaluation clients that need explicit limits.

Acceptance checks:

```bash
GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api ./internal/llm ./internal/qdrant ./internal/retrieval -run 'Test.*(Body|JSON|Limit|Oversize|Response)'
GOCACHE=/tmp/secondcontext-go-cache go test ./...
GOCACHE=/tmp/secondcontext-go-cache go vet ./...
```

Execution record:

- Completion date: 2026-07-20
- Commit: `bfd793e74b41092aa77b01f8b3304bea8efd8d92`
- Tests:
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api ./internal/llm ./internal/qdrant ./internal/retrieval -run 'Test.*(Body|JSON|Limit|Oversize|Response)'` — passed.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./...` — passed.
  - `GOCACHE=/tmp/secondcontext-go-cache go vet ./...` — passed.
  - `git diff --check`, staged diff review, and staged internal-file exclusion check — passed.
- Implementation note: Added one 1 MiB JSON request decoder with stable empty, malformed, trailing-value, and oversized errors. Internal mutation endpoints reject unknown fields while `/v1/responses` remains forward-compatible; response input and metadata now have separate 256 KiB and 64 KiB ceilings. List, search, candidate, and prefetch limits are enforced at API, service, retrieval, and Qdrant boundaries with overflow-safe expansion. OpenAI and Qdrant responses use typed bounded reads (8 MiB and 16 MiB) and no longer expose upstream error bodies. Boundary, propagation, and non-disclosure regressions were added, and the public limits are documented in `README.md`.

### Step 5 — Make outcome updates idempotent and recoverable

- Status: `Completed`
- Findings: SC-006
- Dependencies: Steps 1 and 4
- Suggested commit: `fix(outcomes): make feedback updates recoverable`

Implementation:

1. Document the intended source of truth and state machine for outcome processing before changing code.
2. Introduce a caller-supplied or server-derived stable idempotency key. Persist it with a uniqueness constraint so retries converge on one logical outcome.
3. Put the canonical outcome row and all related Postgres writes that must be atomic into a transaction. Refactor repositories to accept a transaction-capable interface instead of hard-coding `*pgxpool.Pool`.
4. Treat Qdrant indexing and derived person/belief updates as explicit work with durable status (`pending`, `completed`, `failed`) or an equivalent outbox/reconciliation design.
5. Do not discard model/belief update errors. Return them when rollback is possible; otherwise durably record failure, emit structured diagnostics/metrics, and provide a retry/reconciliation path.
6. Prevent duplicate graph edges and memories on retry using stable identifiers or uniqueness constraints.
7. Add failure-injection tests after analysis, memory creation, Qdrant upsert, each graph-edge write, outcome insertion, person-model update, and belief update.
8. Prove that retrying every injected failure reaches one complete outcome without orphaned or duplicated canonical state.
9. Add migration up/down coverage and update architecture, backup/restore, observability, README, and TODO documentation.

Acceptance checks:

```bash
GOCACHE=/tmp/secondcontext-go-cache go test ./internal/outcomes ./internal/memory ./internal/db
GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api -run 'Test.*Outcome'
GOCACHE=/tmp/secondcontext-go-cache go test ./...
GOCACHE=/tmp/secondcontext-go-cache go vet ./...
```

The database-backed failure and retry tests must execute, not skip. Record the integration command and dependency versions in the execution record.

Execution record:

- Completion date: 2026-07-20
- Commit: `4dab7659d72700a28193234ebda4bd3178de7256`
- Tests:
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/outcomes ./internal/memory ./internal/db` — passed.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api -run 'Test.*Outcome'` — passed.
  - `OUTCOME_INTEGRATION=1 POSTGRES_DSN=<local-test-dsn> GOCACHE=/tmp/secondcontext-go-cache go test ./internal/outcomes -run TestOutcomeFailureRetriesConverge -count=1 -v` — passed without skips for all eight injected stages.
  - `POSTGRES_DSN=<local-test-dsn> GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api -run 'Test.*Outcome' -count=1 -v` — passed without skips.
  - `POSTGRES_DSN=<local-test-dsn> POSTGRES_ENABLED=true GOCACHE=/tmp/secondcontext-go-cache go run ./cmd/migrate down 1`, followed by `go run ./cmd/migrate up` and `go run ./cmd/migrate version` — passed; final state `version=2 dirty=false`.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./...` — passed.
  - `GOCACHE=/tmp/secondcontext-go-cache go vet ./...` — passed.
  - `git diff --check`, staged diff review, and staged internal-file exclusion check — passed.
  - Integration dependencies: Go 1.24.10, PostgreSQL 16.13; Qdrant behavior used the deterministic in-process HTTP test double.
- Implementation note: Made Postgres the canonical outcome source before cross-system work. Tenant-scoped caller or derived idempotency keys, request hashes, stable outcome-memory identities, and outcome-scoped graph uniqueness make retries converge. Migration 000002 adds durable pending/completed/failed state and error details for Qdrant, person-model, belief, and overall outcome processing. Repositories support transaction-capable database handles; derived errors are returned and recorded; duplicate model/belief evidence is ignored. Failure injection after analysis, canonical insertion, memory creation, Qdrant upsert, person and belief updates, and each graph edge proves retry convergence to one outcome, one memory, unique edges, and one evidence contribution. README, TODO, architecture, deployment, migration, backup/restore, observability, and recovery documentation were updated.

### Step 6 — Make security integration tests mandatory and visible

- Status: `Completed`
- Findings: SC-007
- Dependencies: Steps 1-5
- Suggested commit: `ci(test): require tenant isolation integration suite`

Implementation:

1. Separate fast unit tests from dependency-backed integration tests using a clear convention such as a build tag plus Make targets.
2. Keep developer-friendly skips only in the fast/unit path. In the integration target, missing or unreachable dependencies must fail immediately.
3. Add a deterministic Compose or CI service setup for Postgres and Qdrant, run migrations, execute all tenant-isolation and cross-store tests, and clean up.
4. Pin service versions and add readiness checks rather than fixed sleeps.
5. Add a CI workflow that runs:
   - formatting verification;
   - `go vet`;
   - unit tests;
   - mandatory integration tests;
   - optionally `go test -race` for packages that exercise concurrent middleware and metrics.
6. Ensure CI output makes executed and skipped test classes explicit.
7. Add Make targets such as `test-unit`, `test-integration`, and `verify`, with `verify` matching required CI checks.
8. Update README, `deploy/README.md`, and TODO with prerequisites and exact local commands.

Acceptance checks:

```bash
make test-unit
make test-integration
make verify
git diff --check
```

Confirm from verbose output that the SC-001 cross-tenant regression and all existing isolation tests ran rather than skipped.

Execution record:

- Completion date: 2026-07-20
- Commit: `e2395a8279dad62bc3e844eb13e072ece03edaaa`
- Tests:
  - `GOCACHE=/tmp/secondcontext-go-cache make test-unit` — passed; output explicitly identified the `-short` lane and its dependency-backed exclusions.
  - `GOCACHE=/tmp/secondcontext-go-cache make test-integration` — passed with pinned Postgres 16.13 and Qdrant 1.15.5 Compose services, readiness checks, migrations, verbose output, and zero skipped tests.
  - `GOCACHE=/tmp/secondcontext-go-cache make verify` — passed formatting verification, `go vet ./...`, unit tests, migrations, and the mandatory integration suite.
  - `GOCACHE=/tmp/secondcontext-go-cache go test ./internal/api ./internal/qdrant` — passed after correcting integration fixtures and preserving typed Qdrant missing-collection classification.
  - `git diff --check`, staged diff review, and staged internal-file exclusion check — passed.
  - Integration runtime: Go 1.24.10, PostgreSQL 16.13, Qdrant 1.15.5.
- Implementation note: Added explicit `test-unit`, `test-integration`, and CI-equivalent `verify` targets. The integration runner provisions isolated pinned services when no DSN is supplied, waits for health rather than sleeping, applies migrations, runs all integration-capable packages verbosely, fails on any skip or dependency error, and cleans up services and volumes. GitHub Actions supplies the same pinned healthy services and makes `make verify` required. The first mandatory run exposed stale derived-update fixtures and a lost Qdrant missing-collection classification; both were corrected, after which the SC-001 cross-tenant response regression and all existing isolation tests ran visibly without skips. README, deployment guidance, and TODO now document the lanes and prerequisites.

### Step 7 — Final remediation verification and documentation audit

- Status: `Completed`
- Findings: All
- Dependencies: Steps 1-6
- Suggested commit: `docs(hardening): align operational guidance` only if public documentation changes are still required

Implementation:

1. Re-read every finding and inspect the final code paths rather than relying only on prior execution records.
2. Run the complete repository verification, including race detection for concurrency-sensitive packages if it is not already part of `make verify`.
3. Review all public docs for stale security, API-limit, proxy, retry, and testing claims.
4. Verify migrations apply and roll back on a clean database.
5. Verify the Docker Compose development flow and health/metrics behavior.
6. Confirm no secrets, local DSNs, generated reports, `PLAN.md`, or `CODE_REVIEW.md` are staged.
7. If no public file needs a change, do not create an empty or record-only commit. Record the verification locally and mark the step completed with `Commit: N/A (verification only)`.
8. If a real public-doc correction is needed, commit only that correction after the standard staged-file exclusion checks.

Acceptance checks:

```bash
make verify
GOCACHE=/tmp/secondcontext-go-cache go test -race ./internal/api ./internal/llm ./internal/retrieval ./internal/scoring
git diff --check
git status --short
git diff --cached --name-only
git diff --cached --name-only | grep -E '(^|/)(PLAN|CODE_REVIEW)\.md$' && exit 1 || true
```

Execution record:

- Completion date: 2026-07-20
- Commit: `N/A (verification only)`
- Tests:
  - `GOCACHE=/tmp/secondcontext-go-cache make verify` — passed formatting verification, `go vet`, the fast `-short` suite, migrations, and the mandatory verbose integration suite with zero skipped tests.
  - `GOCACHE=/tmp/secondcontext-go-cache go test -race ./internal/api ./internal/llm ./internal/retrieval ./internal/scoring` — passed; the initial sandboxed run was blocked when `httptest` could not open an IPv6 loopback listener, then passed with loopback access enabled.
  - On a fresh pinned PostgreSQL 16.13 container, `go run ./cmd/migrate up`, two successive `go run ./cmd/migrate down 1` commands, `go run ./cmd/migrate up`, and `go run ./cmd/migrate version` — passed; final state `version=2 dirty=false`.
  - `docker compose config --quiet` and `docker compose up -d --build --wait` — configuration and image build passed. A host-side `modelsays-postgres` container already owned port 5432, so the endpoint smoke test used the same Compose service definitions on the project network without publishing the Postgres/Qdrant host ports. The API started successfully, `/healthz` returned HTTP 200 with `postgres=ok`, and `/metrics` returned the documented Prometheus metrics. All temporary containers and disposable integration volumes were removed.
  - `git diff --check`, `git status --short`, staged-file review, internal-file exclusion check, and ignored-artifact review — passed. The staging area is empty; only local `PLAN.md` and `CODE_REVIEW.md` records are changed or untracked. Existing ignored `.env` and `.artifacts/` content remains unstaged.
- Implementation note: Re-read all findings and inspected the final identity, authentication configuration, trusted-proxy, bounded-input/upstream-response, outcome-recovery, migration, and integration-test paths against their public documentation. No stale or inaccurate public claim required a tracked-file change, so no empty or record-only commit was created. Final verification visibly executed the cross-tenant response regression and every mandatory integration class without skips.

## Completion checklist

The remediation is complete only when:

- all seven steps are `Completed`;
- SC-001 through SC-007 are marked `Resolved` without removing their original descriptions;
- each implementation step has a commit hash and recorded passing checks;
- mandatory integration output proves tenant-isolation tests executed;
- public documentation matches actual behavior;
- `git diff --cached --name-only` excludes `PLAN.md` and `CODE_REVIEW.md`;
- the internal records remain local and contain the final hashes and status history.
