# Knowledge Bootstrap

An optional, independently deployable FastAPI service for durable reference knowledge.
SecondContext continues to run without this service. This package has its own configuration,
dependencies, migrations, database and authentication; it imports no SecondContext code.

K1 provides source registration, durable ingestion jobs, canonical models and inspection APIs.
K2 adds pasted and uploaded TXT, Markdown, JSON and YAML ingestion. A small in-process worker
polls durable jobs and writes canonical documents without an LLM. Swagger at `/docs` is
available now. Chunking, indexing, search, binary-file/website parsing and the Jinja2 management
UI belong to later milestones.

**Parsed inputs stop at `chunking`, with one processed document and zero chunks.** This means
parsing succeeded and the canonical document is inspectable; it does not mean the source is
indexed/searchable. K5 will consume this stage. `ready` remains reserved for the full pipeline.

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

Qdrant configuration reserves a dedicated `knowledge_chunks` collection. K1/K2 neither connect
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

Creation returns HTTP `202` with `{ "source": {...}, "job": {...} }`. `name` is optional
(default `Untitled`); an unnamed Markdown document uses its first heading as its title.
`format` accepts `auto` (also the default when omitted/null), `text`, `markdown`, `json` or
`yaml` for textual input. The source records the detected format during parsing.

The JSON route also accepts URL/file registrations without content for subsequent milestones.
URL registrations require an HTTP/HTTPS `source_uri`, file registrations require an original
filename, and neither is fetched by K2. Jobs without stored textual input remain pending.

Upload text using the dedicated multipart route, with optional `name` and `format` form fields:

```bash
curl http://localhost:8090/v1/sources/upload \
  -H "Authorization: Bearer $KNOWLEDGE_TOKEN" \
  -H 'Idempotency-Key: uploaded-policy-v1' \
  -F 'file=@policy.yaml' -F 'name=Deployment policy' -F 'format=yaml'
```

Poll `/v1/jobs/{id}` and inspect `/v1/sources/{id}/documents`. The worker processes pasted
and uploaded content identically. It selects parsers from content/explicit override rather than
trusting MIME or extension. Filenames are preserved only as provenance; they never become
filesystem paths. UTF-8 decoded input is retained on the source for retries; original byte
encoding is discarded. Multipart temporary files are closed/removed by the request lifecycle.

| Endpoint | Purpose |
| --- | --- |
| `POST /v1/sources` | Create source and pending job atomically |
| `POST /v1/sources/upload` | Upload one bounded UTF-8 text file |
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
Source/job responses and structured logs omit raw submitted input, database credentials and
tokens. Document inspection intentionally returns normalized text and semantic blocks.

`Idempotency-Key` on source creation is optional, scoped to the authenticated owner, and
limited to 128 characters. Replaying the same payload returns the existing source and original
job; reusing the key with different input returns `409 idempotency_conflict`. The partial
unique job index and row locks allow only one active attempt per source, including concurrent
refresh requests. Source/job state changes commit together.

## Parsing contract and limits

Input must be valid UTF-8; a leading BOM is removed, CRLF/CR are normalized to LF, and binary
control characters are rejected. Paragraph boundaries remain in plain text. Markdown retains
heading ancestry, paragraphs, lists, quotes and fenced/indented code blocks. Code content is
never interpreted as document headings or executed.

JSON/YAML normalize to the same readable, sorted, indented JSON representation. Scalars retain
their types, arrays retain their order, and empty containers/strings remain present. Duplicate
mapping keys are rejected to prevent silent data loss. Blocks carry escaped JSON Pointer paths
(e.g. `/deployment/commands/0`) in `metadata_json.blocks`. Each block contains `type`, `text`,
`heading_path`, optional `level`, and optional `path`; the empty pointer denotes the root.
Documents use a stable source-based URI and SHA-256 of their normalized text.

YAML uses a restricted SafeLoader with PyYAML's YAML 1.1 scalar rules. Dates remain strings.
Custom tags/objects, binary/set values, non-string keys, merge keys, recursive aliases,
multiple YAML documents and non-finite numbers are rejected. Ordinary aliases are supported
within alias, expanded node/depth, scalar-byte and output-size bounds.

Auto detection is conservative and independent of filenames: `{`/`[` starts select JSON
(including malformed candidates); Markdown headings/fences select Markdown; explicit YAML
markers or at least two mapping lines select YAML; plain lists select Markdown; otherwise
input is text. A single `Note: ...` line and scalar `true`/`123` remain text. Use the explicit
format override for ambiguous YAML, JSON scalars or literal Markdown-looking prose.

| Setting | Default | Bounds |
| --- | --- | --- |
| `KNOWLEDGE_MAX_REQUEST_BYTES` | 1 MiB | Whole received HTTP body, including multipart/JSON overhead |
| `KNOWLEDGE_MAX_INPUT_BYTES` | 512 KiB | UTF-8 input before normalization |
| `KNOWLEDGE_MAX_NORMALIZED_BYTES` | 2 MiB | Canonical text plus semantic-block metadata |
| `KNOWLEDGE_MAX_PARSE_DEPTH` | 32 | JSON/YAML nesting, including expanded aliases |
| `KNOWLEDGE_MAX_PARSE_NODES` | 10,000 | Structured/expanded nodes, Markdown tokens or text blocks |
| `KNOWLEDGE_MAX_YAML_ALIASES` | 32 | YAML alias occurrences |

Oversized requests/inputs return `413`; invalid encoding/text returns `422`. Accepted malformed
structured content becomes a durable `failed` job with codes such as `invalid_json`,
`invalid_yaml`, `nesting_too_deep`, `structure_too_large`, `yaml_alias_limit`,
`yaml_recursive_alias`, or `normalized_too_large`. Errors never include parser excerpts.

## Worker and recovery

The API starts one polling thread by default. `KNOWLEDGE_TEXT_WORKER_ENABLED=false` disables
it; `KNOWLEDGE_WORKER_POLL_SECONDS` defaults to 1 second. A worker claims only stored textual
input belonging to configured owners, in `pending`/`parsing`, with at most one found/processed
document and zero chunks. URL and metadata-only registrations are left for later workers.

Workers lock sources before jobs and use `SKIP LOCKED`, so multiple API processes can share
Postgres safely. Parsing and document/progress persistence happen in one bounded transaction;
a crash or database failure rolls back the claim and retries on a later poll/restart. Parser
failures commit a failed attempt. Refresh retries failed jobs using retained input, and upserts
the same canonical document rather than duplicating it. While a parsed job awaits K5 at
`chunking`, refresh reuses that active job. Completed downstream refresh/delete/projection
semantics are subsequent milestones.

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

The operator CLI permits an empty job to reach `ready` for testing the lifecycle; this does
not create documents/chunks. Normal K2 ingestion deliberately stops at `chunking`.
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
migration/model parity. K2 adds parser fixtures, ambiguous detection, hostile structured-input
bounds, uploads, canonical provenance, worker concurrency, transaction rollback and restart
recovery tests.
