# Migrations

This directory holds SQL migrations managed by the in-repo `cmd/migrate` command using `golang-migrate`.

Current migrations:

- `000001_initial_schema.up.sql`
- `000001_initial_schema.down.sql`

Typical workflow:

- `make migrate-up`
- `make migrate-version`
- `make migrate-down`
