# Knowledge Bootstrap

An optional, independently deployable FastAPI service for durable reference knowledge.
SecondContext continues to run without this service. This package has its own configuration,
dependencies, migrations, database and authentication; it imports no SecondContext code.

K1 provides source registration, durable ingestion jobs, canonical models and inspection APIs.
K2 adds pasted and uploaded TXT, Markdown, JSON and YAML ingestion. A small in-process worker
polls durable jobs and writes canonical documents without an LLM. Swagger at `/docs` is
available now. K3 adds digital PDF and DOCX uploads, with page/section provenance and bounded
parser subprocesses. K4 adds public static websites with SSRF-resistant fetching and bounded
page/path/host crawling. Chunking, indexing, search and the Jinja2 management UI belong to
later milestones.

**Parsed inputs stop at `chunking`, with processed documents and zero chunks.** This means
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

Qdrant configuration reserves a dedicated `knowledge_chunks` collection. K1–K3 do not connect
to Qdrant or need embeddings/LLM credentials. Canonical data lives entirely in Postgres.
K3's additive `0002_source_bytes` migration stores original PDF/DOCX bytes on file sources;
apply migrations before starting the updated service. Existing text/document/job rows survive
the upgrade. Downgrading to K2 drops retained binary inputs.

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
filename, and neither is fetched. Jobs without stored text or binary input remain pending.

Upload text using the dedicated multipart route, with optional `name` and `format` form fields:

```bash
curl http://localhost:8090/v1/sources/upload \
  -H "Authorization: Bearer $KNOWLEDGE_TOKEN" \
  -H 'Idempotency-Key: uploaded-policy-v1' \
  -F 'file=@policy.yaml' -F 'name=Deployment policy' -F 'format=yaml'
```

Poll `/v1/jobs/{id}` and inspect `/v1/sources/{id}/documents`. The worker processes pasted
and uploaded text identically. It selects text parsers from content/explicit override rather
than trusting MIME or extension. Filenames are preserved only as provenance; they never become
filesystem paths. UTF-8 decoded input is retained on the source for retries; its original byte
encoding is discarded. Multipart spooled files are closed/removed by the request lifecycle.

Upload a PDF or DOCX on the same route (omit `format`, or explicitly use `pdf`/`docx`):

```bash
curl http://localhost:8090/v1/sources/upload \
  -H "Authorization: Bearer $KNOWLEDGE_TOKEN" \
  -H 'Idempotency-Key: uploaded-handbook-v1' \
  -F 'file=@handbook.pdf' -F 'name=Engineering handbook'
```

PDF magic bytes select PDF; ZIP signatures select a DOCX candidate, whose package is validated
in the parser process. A recognized `.pdf`/`.docx` suffix, matching binary MIME or explicit
binary format must agree with the bytes; conflicts return `422 file_type_mismatch`. Generic,
missing or text MIME/suffixes do not select a binary parser. Thus renaming a ZIP to `.docx`
does not make an arbitrary archive ingestible. ZIP/malformed PDF candidates are accepted
durably, then fail with a stable job error when parsing detects the problem.

Original PDF/DOCX bytes are stored in the source's private Postgres `BYTEA` column, retained
after success/failure for refresh and crash recovery. Their SHA-256 is the source hash; the
document hash covers normalized text. Bytes are omitted from all API views and logs. Binary
idempotency includes their hash. No object storage, persistent filesystem paths or download
endpoint is required. Temporary parser directories use generated names with private access;
they are removed on completion, exception or timeout. An abrupt service/container kill can
leave temporary files until the container's temporary filesystem is removed.

| Endpoint | Purpose |
| --- | --- |
| `POST /v1/sources` | Create source and pending job atomically |
| `POST /v1/sources/upload` | Upload one bounded text, PDF or DOCX file |
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

Text input must be valid UTF-8; a leading BOM is removed, CRLF/CR are normalized to LF, and binary
control characters are rejected. Paragraph boundaries remain in plain text. Markdown retains
heading ancestry, paragraphs, lists, quotes and fenced/indented code blocks. Code content is
never interpreted as document headings or executed.

JSON/YAML normalize to the same readable, sorted, indented JSON representation. Scalars retain
their types, arrays retain their order, and empty containers/strings remain present. Duplicate
mapping keys are rejected to prevent silent data loss. Blocks carry escaped JSON Pointer paths
(e.g. `/deployment/commands/0`) in `metadata_json.blocks`. Each block contains `type`, `text`,
`heading_path`, optional `level`/`path`, and optional `page_start`/`page_end`; the empty pointer
denotes the root.
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
| `KNOWLEDGE_MAX_REQUEST_BYTES` | 10 MiB | Whole received HTTP body, including multipart/JSON overhead |
| `KNOWLEDGE_MAX_INPUT_BYTES` | 512 KiB | UTF-8 input before normalization |
| `KNOWLEDGE_MAX_FILE_BYTES` | 8 MiB | Original PDF/DOCX bytes |
| `KNOWLEDGE_MAX_NORMALIZED_BYTES` | 2 MiB | Canonical text plus semantic-block metadata |
| `KNOWLEDGE_MAX_PARSE_DEPTH` | 32 | JSON/YAML aliases, PDF page trees, DOCX XML/styles/tables |
| `KNOWLEDGE_MAX_PARSE_NODES` | 10,000 | Structured/expanded nodes, Markdown tokens or text blocks |
| `KNOWLEDGE_MAX_YAML_ALIASES` | 32 | YAML alias occurrences |
| `KNOWLEDGE_PARSER_TIMEOUT_SECONDS` | 15 seconds | Binary parser wall time, including startup; CPU capped too |
| `KNOWLEDGE_PARSER_MEMORY_BYTES` | 512 MiB | Binary parser address space |
| `KNOWLEDGE_MAX_PDF_PAGES` | 500 | PDF page count |
| `KNOWLEDGE_MIN_PDF_TEXT_CHARS` | 20 | Minimum alphanumeric characters; see below |
| `KNOWLEDGE_MAX_PDF_STREAM_BYTES` | 8 MiB | Declared/decoded PDF stream sizes where library supports limits |
| `KNOWLEDGE_MAX_DOCUMENT_OBJECTS` | 50,000 | PDF cross-reference/page-tree entries or total DOCX XML elements |
| `KNOWLEDGE_MAX_ARCHIVE_MEMBERS` | 512 | DOCX ZIP members |
| `KNOWLEDGE_MAX_DECOMPRESSED_BYTES` | 32 MiB | Total PDF page-content bytes or DOCX expanded ZIP bytes |
| `KNOWLEDGE_MAX_COMPRESSION_RATIO` | 200 | DOCX maximum expanded/compressed ratio per member |

If an existing deployment pins `KNOWLEDGE_MAX_REQUEST_BYTES` to 1 MiB, raise it to allow larger
file uploads; this bound includes multipart overhead. Text's 512 KiB limit stays independent.

Oversized requests/inputs return `413`; invalid encoding/text returns `422`. Accepted malformed
structured content becomes a durable `failed` job with codes such as `invalid_json`,
`invalid_yaml`, `nesting_too_deep`, `structure_too_large`, `yaml_alias_limit`,
`yaml_recursive_alias`, or `normalized_too_large`. Errors never include parser excerpts.

PDF uses pypdf's strict reader and layout text extraction. Vertical gaps become paragraph
boundaries where the document layout permits; every block has a one-based physical page range.
`metadata_json.page_count` includes blank pages and `pages_with_low_text` lists pages below
the configured minimum. A document below the minimum overall, or with all pages below it,
fails as `pdf_text_unavailable` with an explicit unsupported/scanned/OCR indication. Mixed
documents retain available digital text and expose low-text pages; no OCR or image interpretation
runs. Lower the minimum for legitimate tiny PDFs. Complex columns, fonts and visual layouts
can affect reading order; this MVP does not infer PDF headings or reconstruct PDF tables.
Encrypted PDFs fail as `pdf_encrypted`; corrupt files fail as `invalid_pdf`.

DOCX uses python-docx after validating all ZIP members and XML/relationship parts. It keeps
body paragraphs/tables in document order, heading ancestry (including inherited outline
levels), hyperlink text, list indentation and readable table rows. Bullets use `-`; ordered
lists use deterministic decimal ordinals per numbering ID/level, rather than Word's exact
rendered numbering/restart formats. Table cells are separated by ` | `, cell paragraph breaks
by ` / `; nested tables retain readable text, merged cells emit their text once. DOCX layout
pagination is unavailable, so page fields remain null. Headers/footers, text boxes, comments,
tracked-change-only text and image content are outside this body-text MVP.

ZIP expansion/member/ratio limits apply before loading the document; actual reads are also
bounded and CRC-checked. XML DTD/entities/external entity references, duplicate or traversal
member paths, encrypted archives and macro documents are rejected. External hyperlink targets
are never fetched. Empty DOCX files fail as `docx_text_unavailable`; malformed/unsafe packages
fail as `invalid_docx`. PDF/DOCX resource bounds produce `pdf_resource_limit` or
`docx_resource_limit`; process timeout/OOM/crash produces `parser_timeout` or
`parser_resource_limit`. All are durable failed attempts at the parsing stage.

Binary parsers execute in a fresh process with wall/CPU time, address-space, output-file and
file-descriptor limits on Linux/POSIX. The child gets parsing settings and a minimal environment
without database/authentication credentials. Standard output/error are discarded to avoid
third-party parser excerpts. Timeout kills and reaps the child, removes temporary files and
lets the worker continue with the next job. Resource caps bound library operations that cannot
be checked in advance. Other platforms return `parser_unavailable` for binary jobs; text
ingestion still works. The supported Docker image runs on Linux.

## Website ingestion (K4)

Create a durable URL source through the same authenticated API:

```bash
curl http://localhost:8090/v1/sources \
  -H "Authorization: Bearer $KNOWLEDGE_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: public-handbook' \
  -d '{"kind":"url","source_uri":"https://docs.example.com/docs","name":"Public handbook","config_json":{"scope":"path","max_pages":10,"max_depth":2}}'
```

Omit `config_json` for one page (`scope: page`, `max_pages: 1`, `max_depth: 0`).
`path` includes the seed path and descendants at a slash boundary on the exact hostname;
`/docs` includes `/docs/setup`, but excludes `/docs-other`. `host` includes other paths on
that exact hostname. Both crawl modes default to 10 pages and depth 2. Subdomains and
registrable-domain crawling are excluded. HTTP/HTTPS transitions on that host are allowed;
redirects in crawl modes must remain inside scope. Single-page mode follows public redirects
across hosts, while checking the destination's robots policy before requesting it.
Only absent/`auto`/`html` format hints are accepted. Unknown crawl options, non-integer bounds
and bounds above the server limits are rejected before creating a source/job.

URL identity lowercases scheme/IDNA hostname, removes default ports and fragments, normalizes
unreserved percent escapes and dot segments, and preserves meaningful repeated slashes.
Query strings retain their original order and duplicate parameters; distinct queries are
separate targets. Tracking parameters are not guessed or removed. The original source URL
stays on the source. Links resolve against the observed final URL; `<base>` does not change
fetch scope. Encoded traversal and ambiguous path parameters are excluded in path mode.

Documents use the observed normalized final URL as their stable URI. Metadata includes
requested URL, final URL, HTML `rel=canonical` hint (or final URL), retrieval timestamp, status,
content type, response SHA-256, crawl depth, useful links and semantic blocks. Canonical hints
remain metadata and never authorize another fetch or merge documents. HTML extraction prefers
`main`/`role=main`, then `article`, then the body; removes navigation, headers, footers, scripts,
forms and visibly hidden elements; and retains headings, paragraphs, lists, preformatted code
and readable tables. Text and source snapshot hashes are deterministic. Raw HTML is discarded;
refresh/recovery refetches the URL. Only static HTML/XHTML is supported. Fewer than 40
alphanumeric characters produces `html_text_unavailable`, explaining that JavaScript rendering
or the content may be unsupported. No browser, script execution, LLM or remote asset loading
is involved. Extraction is a conservative DOM heuristic, so unusual layouts can retain chrome
or lose content; CSS-driven visibility and exact visual reading order are not reproduced.

The network policy applies to pages, robots requests, links and every redirect:

- Only HTTP port 80 and HTTPS port 443; no URL credentials, private-network override, proxies,
  cookies, custom headers or inherited authentication. `HTTP_PROXY`/`HTTPS_PROXY` are ignored.
- Resolve both A and AAAA within the request deadline. Reject the entire answer set if any
  address is non-public, loopback, private, link-local, multicast, unspecified, reserved or
  a known metadata/platform address. IPv4-mapped and IPv6 translation/tunnel destinations
  are also denied. DNS search suffixes and host-file aliases are not used.
- Connect directly to a validated numeric address and verify the connected peer. HTTPS uses
  the original hostname for SNI and certificate verification. No second hostname lookup occurs
  during connection. Each redirect is resolved and checked again.
- A total request deadline covers DNS, connections, redirects and reads. Slow trickling headers
  and bodies cannot continually reset it. HTTP header limits also come from the standard client.
  Request `Accept-Encoding: identity`; compressed responses are rejected rather than decompressed.
  Declared and streamed body bytes are bounded. HTTP errors fail without reading their bodies.

Crawls run breadth first with one outstanding request. `max_pages` counts page attempts,
including failed pages and redirect aliases; robots requests and individual redirect hops have
separate bounded work and remain subject to the total crawl deadline. Links are normalized and
deduplicated before scheduling. Robots are fetched once per origin per crawl through the same
safe transport, including for single pages. User agent: `SecondContextKnowledge/0.1`.
The locked Protego parser handles wildcard rules, matching user-agent groups and longest-rule
allow/disallow precedence. `404`/`410` means no robots policy; successful UTF-8 `text/plain`
is parsed. Other statuses, timeouts, invalid encodings, oversized or unsupported responses deny access.
Robots rules apply before every page and redirect. Delay is at least 1 second between requests
to a hostname, including robots/redirects, or the larger robots `Crawl-delay` / `Request-rate`.
Required delays exceeding the remaining budget fail rather than being ignored. Sitemaps,
visit-time schedules, retries, domain expansion and parallel fetching are deferred.

| Setting | Default | Purpose |
| --- | --- | --- |
| `KNOWLEDGE_WEB_REQUEST_TIMEOUT_SECONDS` | 10 | Absolute per-fetch budget, including redirect chain and policy checks |
| `KNOWLEDGE_WEB_CRAWL_TIMEOUT_SECONDS` | 120 | Whole crawl budget; parent allows another 5 seconds for startup |
| `KNOWLEDGE_WEB_MAX_RESPONSE_BYTES` | 2 MiB | Per HTML or robots response |
| `KNOWLEDGE_WEB_MAX_REDIRECTS` | 5 | Per fetch chain |
| `KNOWLEDGE_WEB_MAX_PAGES` | 20 | Maximum source page-attempt limit |
| `KNOWLEDGE_WEB_MAX_DEPTH` | 3 | Maximum source depth, with seed at 0 |
| `KNOWLEDGE_WEB_MAX_LINKS` | 1000 | Links retained/scheduled per HTML page |
| `KNOWLEDGE_WEB_MAX_OUTPUT_BYTES` | 8 MiB | Total normalized crawl output |
| `KNOWLEDGE_WEB_CRAWL_DELAY_SECONDS` | 1 | Minimum request interval per hostname |
| `KNOWLEDGE_WEB_MIN_TEXT_CHARS` | 40 | Minimum extracted alphanumeric characters |

HTML also uses the shared normalized-byte, node and depth limits. The frontier is bounded by
`min(web_max_pages * web_max_links, 5000)`. A disposable crawler process enforces wall time,
CPU, memory, output-file and descriptor limits using the K3 memory setting; no DB/auth settings
or ambient credentials cross that boundary. Timeout kills/reaps the child and cleans private
input/result files. No raw page files are written. As with binary parsing, abrupt container
termination can leave temporary files until the container is removed. Linux/POSIX is required.
The optional Compose service gets 130 seconds to shut down, covering the default crawl bound;
if increasing that bound, increase its stop grace period as well.

Seed failure produces a durable failed job. Failed linked pages are skipped with URL/error code
in source `metadata_json.crawl`; a total timeout/output/resource failure aborts the attempt.
Successful work commits all documents and counters atomically, pauses at `chunking`, and reports
page/frontier limits when reached. Refresh reuses documents by final URL. While awaiting K5,
refresh returns the active job; after a failed/completed job it creates another attempt.
Removal of old pages/chunks/index projections during refresh belongs to K8: existing documents
that were not fetched successfully in a later crawl are currently retained. Multiple deployed
worker processes can each run one crawl; concurrency/delay coordination across replicas is
outside this MVP. No schema migration or Qdrant connection is needed for K4.

## Worker and recovery

The API starts one polling thread by default. `KNOWLEDGE_TEXT_WORKER_ENABLED=false` disables
it (the existing setting controls text, binary and URL jobs); `KNOWLEDGE_WORKER_POLL_SECONDS`
defaults to 1 second. A worker claims stored text or PDF/DOCX input belonging to configured
owners, in `pending`/`parsing`, with at most one found/processed
document and zero chunks. It also claims URL jobs in `pending`/`fetching` with zero counters.
Metadata-only text/file registrations remain pending until they have an input.

Workers lock sources before jobs and use `SKIP LOCKED`, so multiple API processes can share
Postgres safely. Parsing/crawling and document/progress persistence happen in one bounded transaction;
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
not create documents/chunks. Normal K2–K4 ingestion deliberately stops at `chunking`.
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
recovery tests. K3 adds synthetic multi-page/image-only PDFs and a DOCX handbook, with a
fixture generator. Coverage includes page/heading/list/table fidelity, binary persistence and
refresh, malformed files, compressed stream/ZIP expansion abuse, XML entities, parser timeout,
crash cleanup, rollback and worker restart. Fixtures contain no third-party document content.

K4 tests include forbidden address families, mixed DNS answers, pinned peers/TLS hostnames,
redirect and robots safety, wildcard robots rules, real slow HTTP headers/bodies, declared and
streamed byte limits, crawl scope/depth/attempt/frontier bounds, canonical provenance, owner
isolation, idempotency, failed-job retry, atomic rollback and child cleanup. The local HTTP
fixture changes transport only inside tests; production exposes no private-network bypass.
