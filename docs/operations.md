# Operations

## Backup and restore

Postgres is the source of truth. Back up the database with the deployment's normal `pg_dump` workflow before schema upgrades, and restore it before starting the API. The backup must include outcome, memory, graph, and derived-model tables, including their idempotency and processing-status columns.

Qdrant is a derived retrieval index. A Qdrant snapshot reduces recovery time, but it must not replace a newer Postgres backup. After restoring Postgres, reconcile incomplete outcomes before serving retrieval traffic.

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
