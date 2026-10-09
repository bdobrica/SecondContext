# Operations

The Go gateway and optional knowledge service have independent configuration,
databases, collections and migration commands. See [architecture](architecture.md)
and the [knowledge-base guide](knowledge-base.md) for their storage boundaries.

## Backup and restore

Postgres is the source of truth. Back up the database with the deployment's normal `pg_dump` workflow before schema upgrades, and restore it before starting the API. The backup must include outcome, memory, graph, and derived-model tables, including their idempotency and processing-status columns.

Qdrant is a derived retrieval index. Keep a compatible snapshot and plan explicit
reconciliation against the canonical database after restoring. Outcome retries
repair their associated memory points, but the gateway has no general-purpose
memory-index rebuild CLI. The knowledge service has separate rebuild tools below.
After restoring Postgres, reconcile incomplete outcomes before serving retrieval
traffic; a Qdrant snapshot must not replace a newer Postgres backup.

Always test restores in an isolated environment. Apply migrations after restoration and confirm `make migrate-version` reports a clean version.

## Outcome failure inspection and retry

Find incomplete canonical outcomes:

```sql
SELECT id, idempotency_key, processing_status, failed_stage, processing_error, updated_at
FROM interaction_outcomes
WHERE processing_status <> 'completed'
ORDER BY updated_at;
```

Inspect incomplete memory work:

```sql
SELECT id, idempotency_key, qdrant_status, person_model_status, belief_status, processing_error, updated_at
FROM memory_items
WHERE qdrant_status <> 'completed'
   OR person_model_status <> 'completed'
   OR belief_status <> 'completed'
ORDER BY updated_at;
```

Retry the original `POST /interactions/outcome` payload with the same `Idempotency-Key`. Completed stages are skipped and failed stages are attempted again. A 409 means the key belongs to a different payload and must not be bypassed.

Correlate durable failure timestamps with structured HTTP and upstream LLM logs and the `/metrics` request/error counters.


## Service credentials and subject deletion

See [ADR 0001](adr/0001-service-subjects-and-purge.md) and the
[v1 HTTP contract](contracts/service-context-v1.md). Deploy migration 000003 before
the new API. Stop old API instances and offline writers before enabling subject
purge; all live instances sharing stores must use authenticated subject fencing.
Keep each deployment on its intended Qdrant collection; clean retired collections
and backup/provider retention through their separate lifecycle policies.

Each authenticated in-flight request opens one dedicated PostgreSQL connection for
the subject advisory lock in addition to repository pool usage. Budget connections
and limit HTTP concurrency accordingly. Same-subject calls serialize; unrelated
subjects can proceed concurrently. Connection failures/timeouts return bounded
errors; a lost HTTP response is not proof that purge failed or succeeded.

Retry POST `/v1/subjects/purge` for incomplete markers. Operator inspection:

```sql
SELECT external_id, created_at, completed_at
FROM subject_purges
WHERE completed_at IS NULL;
```

Only the pseudonymous external subject and timestamps are retained. Never delete
markers to unblock late requests. Re-registration uses a fresh subject identifier.
Migration 000003's downgrade removes these fences and therefore requires stopping
writers; it does not restore any deleted content. Backups must include the fences
so restoration does not silently resurrect deleted identities.

Gateway subject purge does not delete a separately configured knowledge owner's
sources or candidate audits. Applications using both stores must orchestrate
those lifecycles explicitly.

## Gateway deployment and HTTP controls

Run gateway migrations with `make migrate-up` before the new API; check
`make migrate-version`. `/healthz` checks configured Postgres, but does not prove
Qdrant, inference or the optional knowledge endpoint is usable. Test an
authenticated retrieval/response path separately.

Enable `AUTH_ENABLED` for authenticated use and configure unique `subject=token`
entries. Ambiguous token maps and invalid nonblank boolean values fail startup.
Authenticated subjects determine storage scope; conflicting body selectors return
400 `identity_conflict`. Restricted service credentials are covered by the
[service contract](contracts/service-context-v1.md).

Rate limiting defaults to 60 requests/minute. Forwarded addresses are ignored
unless the immediate peer belongs to `HTTP_TRUSTED_PROXY_CIDRS`; trusted chains
are evaluated right to left. Leave this empty for direct port publication.
Structured HTTP/model logs and Prometheus `/metrics` expose request/latency/error/
token counters. Debug routes exist only in development-like environments.

JSON bodies are limited to 1 MiB and one value; response input/metadata are bounded
to 256/64 KiB. Internal mutation schemas reject unknown fields, while the
OpenAI-compatible response schema permits them. Upstream model and Qdrant bodies
are bounded to 8/16 MiB. See [HTTP limits](../README.md#api-resource-limits) and
[the environment template](../.env.example) for current configuration.

## Knowledge service

The service can use the existing Postgres/Qdrant deployments without restarting
them or the gateway. From the repository root, initialize its separate database,
role, disposable test database and ignored private service `.env` once:

```bash
python3 services/knowledge-bootstrap/scripts/init-db.py
```

The helper defaults to container `secondcontext-postgres-1` and accepts
`--postgres-container NAME`. It refuses to overwrite an existing `.env`. For an
independent deployment, provision a dedicated database/role and use the
[service environment template](../services/knowledge-bootstrap/.env.example).

Build, migrate and start only the optional service:

```bash
docker compose -f docker-compose.yml -f docker-compose.knowledge.yml --profile knowledge build knowledge
docker compose -f docker-compose.yml -f docker-compose.knowledge.yml --profile knowledge run --rm --no-deps knowledge-migrate
docker compose -f docker-compose.yml -f docker-compose.knowledge.yml --profile knowledge up -d --no-deps knowledge
```

Migrations run explicitly, not at API startup. Current head is
`0004_derived_candidates`: earlier revisions add core rows, binary inputs and
chunk/index metadata; the fourth adds audit candidates and retraction triggers.
Back up before upgrades. Local Python development uses `uv sync --locked`,
`make migrate` and `make run` from `services/knowledge-bootstrap`.

Compose exposes port 8090 on localhost (`KNOWLEDGE_HTTP_PORT` overrides it).
`/knowledge` is the management/test UI; `/docs` has API schemas. `/healthz` is
process liveness; `/readyz` checks canonical Postgres, schema revision and tables.
Neither readiness check proves external embeddings/Qdrant are operational. Check
a ready source/search separately. Normal Go Compose startup does not enable Python.

Supply knowledge tokens through `KNOWLEDGE_AUTH_TOKENS` in the service environment.
Root Go settings configure the optional adapter independently; see
[adapter configuration](../README.md#reference-knowledge-adapter-k9). Restart the
relevant application after changing its environment. The add-on gives the service
130 seconds to shut down; increasing crawl/index budgets may require more grace.

### Inspecting and recovering ingestion

Use authenticated source/job/document/chunk APIs or the UI to inspect durable
stages, counters and sanitized failures. `GET /v1/metrics` returns owner-scoped
counts, job failures and ingestion timing from Postgres plus process-local search
counters/latency. Search counters reset on restart and need collection per process.
Deleting sources removes their job aggregates. Logs/metrics do not substitute for
retained job state or candidate audit policy.

| Situation | Action |
| --- | --- |
| Failed fetch/parse/chunk attempt | Correct input/settings, then `POST /v1/sources/{id}/refresh` |
| Failed index write with current canonical chunks | `POST /v1/sources/{id}/reindex` |
| `409 canonical_chunks_outdated` on reindex | Correct the chunking failure and refresh first |
| Paused indexing with `KNOWLEDGE_INDEXING_ENABLED=false` | Re-enable and restart; worker resumes active indexing jobs |
| Lost collection or orphaned projection points | Run owner-scoped rebuild/reconcile from canonical chunks |
| Changed embedding endpoint/model/dimensions/lexical recipe | Choose a fresh collection, rebuild for every owner |
| Changed chunk recipe | Refresh to regenerate chunks; reindex/rebuild retain stored chunks |

Run operator commands from the service directory with its private `.env` and a
configured owner. For host-side commands, override Compose hostnames in `KNOWLEDGE_DATABASE_URL`
and `KNOWLEDGE_QDRANT_URL` with reachable host endpoints. Alternatively, run the
same CLI inside the service container using `/app/.venv/bin/knowledge-bootstrap`.

```bash
uv run --env-file .env knowledge-bootstrap reindex-source SOURCE_UUID --owner local
uv run --env-file .env knowledge-bootstrap rebuild-index --owner local
uv run --env-file .env knowledge-bootstrap reconcile-index --owner local
```

Rebuild and reconcile share one MVP implementation: replay retained chunks, remove
the owner's orphaned source points and report durable failures/nonzero exit.
They preserve other owners and never wipe a live collection. Active parse/chunk
jobs must finish before replay. Unchanged refresh still replays projection points
to repair external loss. Search suppresses non-ready/outdated evidence during work.

Text/file refresh reuses retained input; it does not replace an upload or read an
original local path. Create/check a replacement source before deleting the old
one. Website refresh refetches the scope: complete crawls remove absent documents;
partial/limited crawls preserve unvisited pages, while confirmed linked 404/410s
remove their former documents. Inspect `metadata_json.crawl` for completeness,
missing pages and limit flags. Total retained pages stay within the server bound.

### Knowledge backup and restore

Back up the dedicated database, including retained inputs, semantic blocks,
chunks, jobs, recipe manifests, candidate audits and `alembic_version`. Save owner
credentials and model settings separately through the normal secret-backup
process. Database dumps do not provision cluster roles/passwords. Candidate audit
snapshots can contain source text already deleted from the library.

From the repository root, substitute a private destination:

```bash
umask 077
docker compose exec -T postgres sh -c \
  'exec pg_dump -U "$POSTGRES_USER" --format=custom --no-acl knowledge_bootstrap' \
  > /secure/backups/knowledge.dump
```

Test restoring to a new isolated database, with the knowledge role already present:

```bash
docker compose exec -T postgres sh -c \
  'exec createdb -U "$POSTGRES_USER" -O knowledge_bootstrap knowledge_bootstrap_restore_integration'
docker compose exec -T postgres sh -c \
  'exec pg_restore -U "$POSTGRES_USER" --role=knowledge_bootstrap --no-owner --no-acl --exit-on-error -d knowledge_bootstrap_restore_integration' \
  < /secure/backups/knowledge.dump
```

Check counts, owners, document/chunk hashes and migration revision. Apply newer
migrations if needed. For recovery, stop only the knowledge service while
switching its private database configuration; preserve the old database. Rebuild
each configured owner into a fresh Qdrant collection using the saved embedding/
lexical recipe. Finish active parse/chunk jobs as needed. Verify readiness,
search/provenance and retracted candidate visibility before returning traffic.
The [service runbook](../services/knowledge-bootstrap/README.md#backup-and-restore)
has additional restore details.

### Deletion and candidate audit retention

Authenticated `DELETE /v1/sources/{id}` serializes with writers, removes acknowledged
owner/source-scoped points from the configured and recorded prior collection at
the current Qdrant endpoint, then commits source/document/chunk/job cascades.
An absent source/foreign source or missing collection is idempotently clean.
Backend failure returns `503 deletion_unavailable` and keeps canonical data for
retry. If SQL commit fails after vector removal, retry deletion or reindex the
surviving source. Endpoint changes require separate cleanup of the old endpoint;
there is no endpoint-history outbox. Previously delivered evidence is a read
snapshot, not revoked response content.

Candidate audits survive source deletion as retracted rows. For full audit
erasure, coordinate writers/extractors, collect **all** candidate IDs for the source
using `GET /v1/candidates?source_id=SOURCE_UUID&status=all` with pagination, then
delete each captured ID via `DELETE /v1/candidates/{id}`. Do not advance offsets
while deleting from the same list: first capture all IDs to avoid skipping rows.
Candidate purge returns idempotent 204 and permanently removes its snapshot.
There is no automatic expiry; backups and consumer projections have separate
retention. While a source exists, later explicit extraction may recreate purged
assertions. See [ADR 0005](adr/0005-documentary-candidates.md).

## Verification and release

The gateway's `make verify` checks formatting, vet, unit tests and mandatory real
Postgres/Qdrant integration. `make test-unit` alone excludes dependencies.
`make test-integration` can provision isolated pinned Compose services or use a
supplied isolated `POSTGRES_DSN`. Any skipped integration test fails that lane.
CI uses Postgres 16.13 and Qdrant 1.15.5.

From the knowledge service directory:

```bash
uv sync --locked
make check test
# Export KNOWLEDGE_TEST_DATABASE_URL privately for an isolated database.
# Its name must end in _test or _integration.
export KNOWLEDGE_TEST_QDRANT_URL=http://localhost:6333
make test-integration
```

The knowledge integration lane requires both backends, runs real migrations,
and uses isolated owners/collections and deterministic inference fixtures. It
includes candidate transactional retraction and lifecycle rebuild/delete tests.
Browser smoke checks and the real-embedding corpus evaluation are described in
the [service reference](../services/knowledge-bootstrap/README.md#verify) and
[evaluation guide](evaluation.md#knowledge-base-evaluation).

Images are independently versioned as `quay.io/bdobrica/secondcontext-gateway`
and `quay.io/bdobrica/secondcontext-knowledge` (the latter includes API/worker/UI).
Manual committed version increases in their `.bumpversion.cfg` files trigger
publication on a push to `main`; ordinary source changes and initial config
creation do not. The workflow publishes version tags and `latest` for amd64/arm64
after the configured `QUAY_AUTH` credential is available. Pin versions/digests
for deployments; publishing does not migrate or restart services. See
[the release workflow](../README.md#publishing-docker-images).
