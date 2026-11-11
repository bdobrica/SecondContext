# Migrations

This directory holds SQL migrations managed by the in-repo `cmd/migrate` command using `golang-migrate`.

Current migrations:

- `000001_initial_schema.up.sql`
- `000001_initial_schema.down.sql`
- `000002_outcome_processing.up.sql`
- `000002_outcome_processing.down.sql`
- `000003_subject_purges.up.sql`
- `000003_subject_purges.down.sql`
- `000004_linked_projections.up.sql` / `.down.sql`
- `000005_conversation_history.up.sql` / `.down.sql`
- `000006_conversational_learning.up.sql` / `.down.sql`

Typical workflow:

- `make migrate-up`
- `make migrate-version`
- `make migrate-down`

Gateway Docker images bundle the same command at `/app/migrate` and all SQL files
under `/app/migrations`. From the standard Compose deployment:

```bash
docker compose run --rm --no-deps migrate
docker compose run --rm --no-deps migrate /app/migrate version
```

The `migrate` service defaults to `up` and runs before the API on Compose startup.
It uses the gateway database credentials and needs no host Go installation,
source mount, Qdrant or model-provider configuration. Keep the image's `/app`
working directory when invoking the CLI; it resolves `migrations` from there.
Repeated `up` is safe when the schema is already current. See
[deployment](../deploy/README.md#deploying-alongside-another-application) for an
example using published images alongside another application.

Migration `000002` adds tenant-scoped idempotency constraints and durable processing state for outcomes and outcome memories. Its down migration discards that recovery history, so take a Postgres backup before rolling it back.

Migration `000003_subject_purges` adds durable subject deletion fences. Its down
migration removes the fences, so stop writers before rollback. It never restores
deleted content. See [operations](../docs/operations.md).

Migration `000006_conversational_learning` adds per-subject learning settings,
durable capture/replay receipts, and observation derivation links. No account is
enabled by migration. Stop learning workers before rolling it back: the down
migration removes settings and receipts while retaining existing memory rows;
re-enabling later loses replay/forgetting protection for those old occurrences.
