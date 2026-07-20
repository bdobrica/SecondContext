# Migrations

This directory holds SQL migrations managed by the in-repo `cmd/migrate` command using `golang-migrate`.

Current migrations:

- `000001_initial_schema.up.sql`
- `000001_initial_schema.down.sql`
- `000002_outcome_processing.up.sql`
- `000002_outcome_processing.down.sql`

Typical workflow:

- `make migrate-up`
- `make migrate-version`
- `make migrate-down`

Migration `000002` adds tenant-scoped idempotency constraints and durable processing state for outcomes and outcome memories. Its down migration discards that recovery history, so take a Postgres backup before rolling it back.
