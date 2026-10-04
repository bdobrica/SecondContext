# Knowledge Bootstrap

An optional, independently deployable FastAPI service for durable reference knowledge.
SecondContext continues to run without this service. This package has its own configuration,
dependencies, migrations, database and authentication; it imports no SecondContext code.

K1 provides source registration, durable ingestion jobs, canonical models and inspection APIs.
Sources and jobs are persisted together in one transaction. **No ingestion worker is running
yet:** jobs remain `pending` until a worker in later milestones processes them, or an operator
exercises the state machine. Parsing, uploads, fetching, chunking, indexing, search and the
Jinja2 management UI are subsequent milestones. Swagger at `/docs` is available now.

## Run alongside the existing SecondContext stack

From the repository root, initialize a separate database and role on the existing Postgres
container. This also creates a disposable test database and an ignored service `.env` with
generated database credentials and a bearer token for the `local` owner:

```bash
python3 services/knowledge-bootstrap/scripts/init-db.py
```

The default Postgres container name is `secondcontext-postgres-1`; override it with
`--postgres-container NAME`. This is a one-time initialization. It refuses to overwrite an
existing `.env`. It does not modify SecondContext's database or credentials.

Build, migrate and start **only** the optional service:

```bash
docker compose -f docker-compose.yml -f docker-compose.knowledge.yml --profile knowledge build knowledge
docker compose -f docker-compose.yml -f docker-compose.knowledge.yml --profile knowledge run --rm --no-deps knowledge-migrate
docker compose -f docker-compose.yml -f docker-compose.knowledge.yml --profile knowledge up -d --no-deps knowledge
```

The API is at `http://localhost:8090`, with interactive API documentation at
`http://localhost:8090/docs`. Existing SecondContext, Postgres and Qdrant containers remain
running. The add-on uses the existing Compose network and the `postgres`/`qdrant` hostnames.
Set `KNOWLEDGE_HTTP_PORT` to choose another host port.

Normal `docker compose up` / `make run` for SecondContext never starts or requires Python.
To stop only the optional API:

```bash
docker compose -f docker-compose.yml -f docker-compose.knowledge.yml --profile knowledge stop knowledge
```

For a fresh combined stack, after provisioning the knowledge database, Compose can also run
the `knowledge-migrate` dependency automatically using `--profile knowledge up -d`.

## Run independently

Provision a dedicated Postgres database and fill in the values from `.env.example`. In a shell
where Postgres is reachable on the host, use `localhost` instead of the Compose hostname
`postgres`. Settings use the `KNOWLEDGE_` prefix and are validated at startup. Migrations need
only `KNOWLEDGE_DATABASE_URL`; the API additionally requires `KNOWLEDGE_AUTH_TOKENS`.

```bash
cd services/knowledge-bootstrap
uv sync --locked
# Supply your own .env using .env.example as a reference.
make migrate
make run
```

Database URLs use `postgresql+psycopg://`. Sync SQLAlchemy sessions are scoped per request;
FastAPI runs the database endpoints in its thread pool. Database pool size, connection/query
timeouts, request size and log level are configurable. `/healthz` reports process liveness;
`/readyz` verifies database access, the expected migration revision and the canonical tables.
Migrations run explicitly rather than competing across API processes on startup.

Qdrant configuration reserves a dedicated `knowledge_chunks` collection. K1 neither connects
to Qdrant nor needs embeddings/LLM credentials. Canonical data lives entirely in Postgres.

## Ownership and API

`KNOWLEDGE_AUTH_TOKENS` is a JSON object mapping opaque owner/workspace IDs to distinct bearer
tokens. Tokens must have at least 16 characters and no whitespace. There is no identity table
and no caller-selectable owner field/header. The credential determines ownership. All `/v1`
endpoints require it; cross-owner lookups return the same `404` as missing resources.
Composite foreign keys enforce matching owners and provenance even on direct database writes.

Use the generated token from the ignored service `.env` as `KNOWLEDGE_TOKEN` in your shell,
or paste it into the **Authorize** dialog at `/docs`:

```bash
curl http://localhost:8090/v1/sources \
  -H "Authorization: Bearer $KNOWLEDGE_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: handbook-v1' \
  -d '{"kind":"text","name":"Engineering handbook","format":"markdown","text":"# Deployments\nProduction requires two approvers."}'
```

Creation returns HTTP `202` with `{ "source": {...}, "job": {...} }`. Input text is stored
durably but not parsed. `kind` accepts `text`, `url` and `file`; URL registrations require an
HTTP/HTTPS `source_uri`, and file registrations require an original filename in `source_uri`.
File registration is metadata only in K1, not an upload. Unknown format can be omitted/null;
format detection is K2. Registering a URL makes no outbound request.

| Endpoint | Purpose |
| --- | --- |
| `POST /v1/sources` | Create source and pending job atomically |
| `GET /v1/sources` | List this owner's sources |
| `GET /v1/sources/{id}` | Inspect source |
| `POST /v1/sources/{id}/refresh` | Reuse active job, or queue a fresh attempt |
| `GET /v1/sources/{id}/jobs` | Inspect attempt history |
| `GET /v1/jobs/{id}` | Inspect status, progress and errors |
| `GET /v1/sources/{id}/documents` | Inspect canonical documents |
| `GET /v1/documents/{id}` | Inspect a canonical document |
| `GET /v1/documents/{id}/chunks` | Inspect chunks and provenance |

List endpoints support `limit` (default 50, maximum 100) and `offset`. Errors use
`{"error":{"code":"...","detail":"..."}}`; validation errors also name invalid fields.
Responses and structured logs omit raw submitted input, database credentials and tokens.

`Idempotency-Key` on source creation is optional, scoped to the authenticated owner, and
limited to 128 characters. Replaying the same payload returns the existing source and original
job; reusing the key with different input returns `409 idempotency_conflict`. The partial
unique job index and row locks allow only one active attempt per source, including concurrent
refresh requests. Source/job state changes commit together.

## Exercise the state machine

Transitions belong to workers/operators, not external API callers. The CLI takes an explicit
configured owner and a job UUID. For a text/file source the normal order is:

```text
pending -> parsing -> chunking -> indexing -> ready
```

URL sources insert `fetching` between `pending` and `parsing`. Any active state can fail.
Progress counters cannot decrease, processed documents cannot exceed discovered documents,
and all discovered documents must be processed before `ready`. A failed job retains the last
stage and must include a stable error code and detail. Exact repeated updates are idempotent;
finished jobs cannot be changed. Refresh starts a new job after a terminal attempt and keeps
the old job for inspection. `last_ingested_at` changes only on success.

For the running optional container:

```bash
docker compose -f docker-compose.yml -f docker-compose.knowledge.yml --profile knowledge exec knowledge \
  /app/.venv/bin/knowledge-bootstrap transition-job JOB_UUID parsing --owner local --documents-found 1
```

For a local process (from this service directory):

```bash
uv run --env-file .env knowledge-bootstrap transition-job JOB_UUID parsing --owner local --documents-found 1
uv run --env-file .env knowledge-bootstrap transition-job JOB_UUID failed --owner local \
  --error-code parser_failed --error-detail 'Input could not be parsed'
```

K1 permits an empty job to reach `ready` for testing the lifecycle; this does not create any
documents or chunks. Successful real ingestion comes with later parser/indexing workers.
Job retention currently follows source retention: the schema cascades sources to their
documents, chunks and jobs. Public deletion and projection cleanup are later lifecycle work.

## Verify

```bash
cd services/knowledge-bootstrap
uv sync --locked
make check test
# Use the generated database role/password with the separate test database on the host.
export KNOWLEDGE_TEST_DATABASE_URL='postgresql+psycopg://USER:PASSWORD@localhost:5432/knowledge_bootstrap_test'
make test-integration
```

The integration command fails if the database setting is missing/unreachable; it does not
silently skip Postgres. Database names must end in `_test` or `_integration`. Tests run the
actual Alembic migrations, use unique owners, and delete only their own rows. Migration
upgrade/downgrade checks run in an isolated temporary schema. The separate CI workflow runs
all tests against Postgres without SecondContext or Qdrant.

Dependency versions are committed in `uv.lock`; Docker and CI install the frozen lock.
Tests cover source/job atomicity, legal transitions, monotonic progress, failure/retry,
idempotency under concurrent requests, owner access, database provenance constraints and
migration/model parity.
