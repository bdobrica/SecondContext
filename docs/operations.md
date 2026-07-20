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
