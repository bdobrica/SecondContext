# Knowledge Bootstrap

An optional, independently deployable FastAPI service for durable reference knowledge.
SecondContext continues to run without this service. This package has its own configuration,
dependencies, migrations, database and authentication; it imports no SecondContext code.

K1 provides source registration, durable ingestion jobs, canonical models and inspection APIs.
K2 adds pasted and uploaded TXT, Markdown, JSON and YAML ingestion. A small in-process worker
polls durable jobs and writes canonical documents without an LLM. Swagger at `/docs` is
available now. K3 adds digital PDF and DOCX uploads, with page/section provenance and bounded
parser subprocesses. K4 adds public static websites with SSRF-resistant fetching and bounded
page/path/host crawling. K5 adds structure-aware chunks and dense/sparse Qdrant indexing,
with projection-only retry and rebuild tools. K6 adds filtered hybrid retrieval with canonical
evidence and score debugging. K7 adds a Jinja2 management and test UI at `/knowledge`,
with ingestion, inspection, refresh/reindex, confirmed deletion and ranked evidence.
K8 completes website refresh cleanup, unchanged parser/chunk reuse, owner-scoped operational
metrics, backup/recovery guidance and a real Postgres/Qdrant lifecycle evaluation.
K10 adds explicit, optional documentary extraction with chunk-backed candidates and
transactional retraction. It does not promote candidates into a consumer’s cognitive model.

**Jobs now reach `ready` after canonical chunks and acknowledged index writes.** Configure
an embedding endpoint and the dedicated Qdrant collection below. Setting
`KNOWLEDGE_INDEXING_ENABLED=false` keeps parsing/chunking usable without either backend;
jobs then pause at `indexing` with inspectable chunks, and resume when indexing is enabled.

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

Canonical data lives entirely in Postgres. K5 uses a dedicated `knowledge_chunks` Qdrant
collection, independently of SecondContext's memory collection. Apply migrations before
starting the updated service. `0002_source_bytes` retains PDF/DOCX bytes;
`0003_chunk_metadata` adds semantic chunk metadata and pins each projection's embedding/lexical
recipe. `0004_derived_candidates` adds a candidate audit table and evidence-retraction
triggers. Existing sources, documents and jobs survive the upgrade. The worker automatically
continues K2–K4 jobs paused at `chunking`. Downgrade drops the fields introduced by that revision.

## Ownership and API

`KNOWLEDGE_AUTH_TOKENS` is a JSON object mapping opaque owner/workspace IDs to distinct bearer
tokens. Tokens must have at least 16 characters and no whitespace. There is no identity table
and no caller-selectable owner field/header. The credential determines ownership. All `/v1`
endpoints require it; cross-owner lookups return the same `404` as missing resources.
Composite foreign keys enforce matching owners and provenance for sources/documents/chunks
even on direct database writes. K10 candidate audit snapshots deliberately survive those
rows and are owner-scoped by the extraction and inspection APIs.

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
| `GET /v1/sources` | List this owner's sources, canonical counts and latest job/error |
| `GET /v1/sources/{id}` | Inspect source, canonical counts and latest job/error |
| `DELETE /v1/sources/{id}` | Delete source, descendants, jobs and source-scoped search points |
| `POST /v1/sources/{id}/refresh` | Reuse active job, or queue a fresh attempt |
| `POST /v1/sources/{id}/reindex` | Queue projection-only recovery from canonical chunks |
| `POST /v1/search` | Retrieve ranked canonical evidence |
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
Successful parsing commits all documents and counters atomically, then K5 creates chunks and
indexes them. Crawl metadata reports page/frontier limits. Refresh reuses documents by final
URL and returns any active job; after a failed/completed job it creates another attempt.
K8 removes absent documents after an exhausted successful crawl, and confirmed linked
404/410 responses remove their previous documents. Limited or partially failed crawls
retain unvisited pages; stale chunks and obsolete index points are removed after projection
succeeds. Multiple deployed
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
the same canonical document rather than duplicating it. Chunking commits canonical chunks
and `indexing` progress in a separate transaction. Projection writers serialize by owner
using Postgres advisory locks, then lock the source/job in the same order as refresh.
A configured owner can index one source at a time; other owners can proceed independently.
Refresh reuses any active job; deletion is serialized with projection and ingestion writers.

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
not create documents/chunks. Normal ingestion runs the full K2–K5 pipeline.
Job retention currently follows source retention: the schema cascades sources to their
documents, chunks and jobs. Public deletion and projection cleanup are later lifecycle work.

## Verify

```bash
cd services/knowledge-bootstrap
uv sync --locked
make check test
# Use the generated database role/password with the separate test database on the host.
export KNOWLEDGE_TEST_DATABASE_URL='postgresql+psycopg://USER:PASSWORD@localhost:5432/knowledge_bootstrap_test'
export KNOWLEDGE_TEST_QDRANT_URL=http://localhost:6333
make test-integration
```

The integration command requires both Postgres and Qdrant settings and reachable backends;
it does not silently skip either. Database names must end in `_test` or `_integration`. Tests run the
actual Alembic migrations, use unique owners, and delete only their own rows. Migration
upgrade/downgrade checks run in an isolated temporary schema. The separate CI workflow runs
all tests against dedicated Postgres and Qdrant containers, without SecondContext or paid
inference. Real Qdrant tests use deterministic test embeddings; other backend tests mock HTTP.

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

## Chunking and indexing (K5)

Every parser emits the same `ParsedDocument`/`Block` representation, defined in
`representation.py`. Ingestion binds title, stable URI and format to the canonical document;
blocks retain heading ancestry, JSON Pointer paths and PDF page ranges. Chunking reads this
stored representation and does not reparse source bytes or fetch URLs.

Defaults are a target of **600 tokens** and a hard maximum of **1,200 tokens**, counted with
`tiktoken`'s fixed `cl100k_base` encoding. Heading ancestry defines section boundaries; tiny
sections remain separate, while paragraphs in the same section pack together. PDF/plain text
pack paragraphs and retain the min/max page range. Code, tables, lists and structured values
stay intact unless the hard limit requires a split. Long unstructured paragraphs alone repeat
up to **40 suffix tokens**. Chunks also have a 16,384-character ceiling; splitting tokenizes
4,096-character windows to bound pathological long-word BPE work, then recounts each emitted
chunk exactly. Unicode splits are lossless. No LLM normalization or blind section overlap is
used. Docker preloads the tokenizer vocabulary so runtime chunking needs no download; local
runs download/cache it on first use.

Each chunk stores text, ordinal, token count, heading/page provenance, block indexes/types and
structured paths, actual overlap counts, and the versioned recipe. A SHA-256 hash covers text
and heading/page location; UUIDv5 identities include document ID, hash and duplicate occurrence.
Unchanged chunks retain IDs across refreshes and projection retries. Changing the global recipe
requires **refresh** to re-chunk; reindex/rebuild only replay stored chunks.

Set these values in the service's own `.env` (the Go service's environment is not read):

```dotenv
KNOWLEDGE_EMBEDDING_BASE_URL=https://api.openai.com/v1
KNOWLEDGE_EMBEDDING_API_KEY=YOUR_EMBEDDING_KEY
KNOWLEDGE_EMBEDDING_MODEL=text-embedding-3-small
KNOWLEDGE_EMBEDDING_DIMENSIONS=1536
KNOWLEDGE_QDRANT_URL=http://qdrant:6333
KNOWLEDGE_QDRANT_COLLECTION=knowledge_chunks
```

The [embedding API contract](https://developers.openai.com/api/reference/resources/embeddings/methods/create)
is OpenAI-compatible: batched strings, float vectors and indexed results. Alternate providers
can use the same contract; authentication is sent only to the configured embedding endpoint.
`KNOWLEDGE_EMBEDDING_REQUEST_DIMENSIONS` is optional, for providers/models supporting an explicit
output dimension. Returned dimensions must match `KNOWLEDGE_EMBEDDING_DIMENSIONS`.

Qdrant holds named `dense` (Cosine) and `sparse` vectors. Sparse encoding is deterministic
Unicode case-folded lexical log-TF with 32-bit SHA-256 token hashes; Qdrant's `idf` modifier
supplies corpus weights. It needs no learned sparse model or downloaded vocabulary. Hash
collisions are possible, and this baseline has no stemming/semantic expansion. K6 uses
this same function for queries and includes a small retrieval benchmark. Payloads retain owner/source/
document/chunk IDs, title/URI/canonical URL, heading/page/format/hash and projection generation;
canonical text stays in Postgres. Keyword indexes support owner/source/document filtering.

The service creates missing collections and validates existing dense/sparse configuration.
Postgres pins the embedding endpoint/model/dimensions and lexical recipe **before external
writes**. Changing any of these requires a new collection name and rebuild, even when vector
dimensions match. Credentials can rotate without changing the recipe. Use a dedicated
collection; never point this service at SecondContext's memory collection. A restored canonical
database must preserve this manifest along with its chunks.

| Setting (`KNOWLEDGE_` prefix) | Default | Purpose |
| --- | --- | --- |
| `CHUNK_TARGET_TOKENS` | 600 | Paragraph packing target |
| `CHUNK_MAX_TOKENS` | 1200 | Exact hard chunk token ceiling |
| `CHUNK_OVERLAP_TOKENS` | 40 | Oversized unstructured paragraph suffix only |
| `MAX_CHUNKS_PER_SOURCE` | 5000 | Canonical/projection source bound |
| `INDEXING_ENABLED` | true | Disable external writes and pause at indexing |
| `INDEX_BATCH_SIZE` | 32 | Embedding and upsert batch size |
| `INDEX_TIMEOUT_SECONDS` | 10 | Backend HTTP operation timeout |
| `INDEX_SOURCE_TIMEOUT_SECONDS` | 120 | Projection elapsed-time budget checked between operations/reads |

HTTP responses are bounded to 16 MiB, redirects/proxy environment are disabled, identity
encoding is requested and compressed responses rejected. Errors expose stable sanitized codes,
never backend bodies or credentials. The elapsed budget does not preempt a blocked socket;
its operation timeout bounds that final wait. Increase Compose shutdown grace if increasing
crawl/index timeouts. Readiness checks canonical Postgres/schema, so backend outages do not
hide inspectable data or prevent the core API starting.

### Failure recovery and rebuild

Commit order is **documents -> chunks/indexing job -> recipe manifest -> Qdrant -> ready**.
All current points upsert by canonical chunk UUID with `wait=true`; only after all succeed does
an owner/source/generation filter remove obsolete points. A backend failure commits a durable
`failed` attempt at stage `indexing`, with chunks and counters intact. A database failure after
external success leaves the old indexing job retryable, and repeating its upserts/deletion is
idempotent. Projection batches are not atomically visible across stores; K6 validates
returned IDs/hashes, generation and recipe against canonical rows and source readiness.

Retry indexing without refetching, reparsing or rechunking:

```bash
curl -X POST -H "Authorization: Bearer $KNOWLEDGE_TOKEN" \
  http://localhost:8090/v1/sources/SOURCE_UUID/reindex
```

The endpoint is owner-scoped, returns the active job if one exists, otherwise creates a new
indexing attempt. Finished attempts remain immutable. Without canonical chunks it returns
`no_canonical_chunks`; use refresh to retry earlier stages. Re-enabling indexing automatically
consumes paused indexing jobs; failed attempts need an explicit retry.

Operator tools (from the service directory):

```bash
uv run --env-file .env knowledge-bootstrap reindex-source SOURCE_UUID --owner local
uv run --env-file .env knowledge-bootstrap rebuild-index --owner local
uv run --env-file .env knowledge-bootstrap reconcile-index --owner local
```

Rebuild and reconcile intentionally share one MVP implementation: replay every retained
canonical chunk for the selected configured owner, then remove that owner's orphaned source
points. They recreate a lost collection without original files/websites, remove obsolete
same-source points, and preserve other owners. Durable jobs expose any failure; a non-ready
job or cleanup failure gives a nonzero exit. Active parse/chunk jobs are reused and may require
finishing before rerunning rebuild. Back up Postgres; the search projection is disposable.
For a new embedding model, choose a fresh collection and replay each owner. Neither command
wipes a live collection. K8 completes absent-crawl-page lifecycle handling; K7 supplies
the serialized deletion path.

K5 tests cover tiny/large/deep sections, Unicode, code/tables, page ranges, structured paths,
overlap and determinism; real Postgres tests cover stage commits, partial index failure,
projection-only retry, owner isolation, stable IDs, concurrency and rollback after external
success. Real Qdrant tests cover dense/sparse queries, stale filters, preserving other owners,
and rebuilding a deleted test collection:

```bash
export KNOWLEDGE_TEST_QDRANT_URL=http://localhost:6333
uv run pytest --require-postgres --require-qdrant
```

## K6: hybrid retrieval API

Search canonical reference evidence without database or Qdrant access:

```bash
curl http://localhost:8090/v1/search \
  -H "Authorization: Bearer $KNOWLEDGE_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"query":"How do we roll back a failed production deployment?","limit":5,"debug":true,"filters":{"source_ids":[],"document_ids":[],"formats":[]}}'
```

The credential selects the owner; caller-supplied owner fields are rejected. `query` must be
nonblank, contain no NUL, and fit both 2,048 characters and 1,024 `cl100k_base` tokens.
`limit` defaults to 5, with a configurable server maximum of 20 (absolute maximum 100).
Filter lists use OR within a field and AND across fields. Empty lists impose no restriction.
`source_ids` and `document_ids` each accept at most 100 UUIDs. `formats` accepts `html`,
`pdf`, `docx`, `markdown`, `json`, `yaml`, and `text`. Unknown filters are rejected.
`tags: []` reserves the extension; nonempty tags fail validation until tagging is implemented.
A filter referring to another owner's resource yields no evidence from that resource.

Responses contain `results`, each with canonical `chunk_id`, `document_id`, `source_id`,
`score`, `text`, `title`, `heading_path`, `uri`, `source_uri` and `format`. `page_start` and
`page_end` appear when present. `uri` identifies the parsed document; `source_uri` identifies
the original upload/reference when supplied. Null optional fields are omitted. With
`debug: true`, `score_components` reports dense/sparse ranks and raw scores, normalized
fusion, title/heading and numeric-identifier term overlap, relevance and the applied diversity
multiplier.

The default `mode: "hybrid"` embeds the query with the pinned K5 embedding model and encodes
lexical terms with the same Unicode/log-TF sparse recipe used for documents. Qdrant returns
both named-vector candidate lists through its
[batch query API](https://api.qdrant.tech/v-1-15-x/api-reference/search/query-batch-points).
`mode: "dense"` and `mode: "sparse"` isolate either path for diagnosis/evaluation. Sparse-only
search makes no embedding request; a punctuation-only sparse query returns no results.

Ranking uses equal-weight reciprocal rank fusion with `k=60`, normalized against the
requested modes. The inexpensive knowledge reranker combines 82% fusion, 12% title term
coverage and 6% heading term coverage. When query terms contain numeric identifiers
(e.g. `429`, `INC-742`, `ZQ-91`), 10 percentage points move from fusion to exact coverage
of those terms in the chunk/title. This keeps generic titles from outranking specific
status/error/worker references. Terms are case-folded Unicode words. The score is a
ranking heuristic in [0,1], **not a confidence probability**, and raw dense/sparse scores
are not directly comparable. There is no recency decay or freshness penalty. Configured
source-priority controls and optional freshness weighting remain future extensions; this
version does not assign implicit priority from arbitrary source metadata.

Selection greedily discounts repeated documents/sections by
`1 / (1 + 0.25 * document_count + 0.5 * section_count)`. This promotes alternative sections
and documents while allowing several useful passages from a single handbook. Evidence with
at least 85% Jaccard similarity of case-folded word 5-grams is deduplicated across documents;
short passages deduplicate by their whole normalized word sequence. Final scores include
the diversity multiplier. Ties resolve by canonical chunk UUID.

Postgres supplies evidence and provenance. Each candidate must match the authenticated
owner, source/document IDs, chunk hash and the source's committed projection generation,
collection and recipe, and the source must currently be `ready`. Backend payload text/title/
URI is never evidence. Missing, orphaned, stale, partially indexed or cross-owner points
are excluded. Canonical reads use one joined snapshot after backend retrieval; a refresh
committed before that read hides the previous projection. A later refresh/deletion can still
invalidate an already delivered response, as with any read API. There are no search-time
writes, collection creation or index repairs, and existing ready evidence remains searchable
when ingestion indexing is disabled.

`KNOWLEDGE_SEARCH_CANDIDATE_LIMIT` defaults to 100 per mode, capped at 200 and required to
cover `KNOWLEDGE_SEARCH_MAX_LIMIT`. Bounded overfetch precedes canonical validation and
selection; stale points or deduplication can yield fewer results than requested. The service
does not scan beyond this budget or claim that every matching passage is considered.
`KNOWLEDGE_SEARCH_TIMEOUT_SECONDS` defaults to 20, capped at 60, and bounds backend elapsed
time; the existing HTTP operation timeout and response-byte ceiling also apply. As for
indexing, an in-flight socket operation can finish its remaining operation timeout before
an elapsed-budget failure is observed. Database statements retain their configured timeout.
Canonical hydration excludes original source input, full document text and unrelated metadata.

An owner with no ready sources in the configured collection receives an empty result list
without backend requests. When ready evidence exists, missing/incompatible manifests produce
`503 index_configuration_mismatch`. Backend failures, missing collections or invalid responses
produce sanitized `503 search_unavailable`; backend timeouts produce `503 search_timeout`.
Hybrid requests fail explicitly if either required backend path fails. Invalid requests return
`422`; a limit over the server setting returns `search_limit_exceeded` and excess query tokens
return `query_too_large`. Authentication uses the existing `401` contract. No backend secrets,
request URLs or response bodies enter these errors.

### Small retrieval benchmark

[`benchmarks/corpus.json`](benchmarks/corpus.json) contains 16 one-section reference documents
and ten labeled queries: paraphrased rollback, exact worker/incident codes, mixed intents,
authentication, backup retention, scanned PDFs and Unicode lexical encoding.
[`benchmarks/results.json`](benchmarks/results.json) records the 2026-10-06 live run through
`POST /v1/search` with real `text-embedding-3-small` 1,536-dimensional embeddings and Qdrant
1.15.5. Results identify fixture keys rather than runtime UUIDs or credentials.

| Mode | Mean precision@3 | Mean precision@1 |
| --- | --- | --- |
| Dense | 0.4333 | 1.0000 |
| Sparse | 0.4000 | 0.8000 |
| Hybrid | 0.4333 | 0.9000 |

Hybrid recovered release rollback for `Undo faulty software push`, which has no lexical
word overlap with the corpus and yielded no sparse-only evidence. Dense and hybrid recovered
every labeled relevant document in the top three on this small corpus. Dense-only had better
top-one precision; hybrid remains a baseline rather than a claim of universal improvement.
Several queries have only one labeled relevant document, so their maximum
precision@3 is 1/3; precision always divides by the requested `k`, including short responses.
These measurements are a smoke baseline, not a general retrieval-quality guarantee. Sparse
IDF statistics apply to the whole collection, and tied backend candidates can change order
when fixture UUIDs or other indexed data change.

To repeat against your own ingested copy, upload each document as Markdown using its title
as the first heading and save a JSON object mapping corpus `key` to the accepted source UUID.
Wait for all jobs to become ready, then run from this package directory:

```bash
KNOWLEDGE_TOKEN="$KNOWLEDGE_TOKEN" .venv/bin/python benchmarks/evaluate.py \
  --url http://localhost:8090 --sources /tmp/benchmark-source-map.json \
  --output /tmp/benchmark-results.json --k 3
```

The evaluator filters every request to that corpus, checks provenance fields and canonical
titles, and reports each mode's precision and per-query improvements. It uses HTTP only,
never creates/deletes sources, and requires no database credentials. K6 adds no migrations
or dependencies; existing K5 indexes can serve searches immediately. Benchmark fixtures in
the recorded live run were removed together with their owner-scoped Qdrant points. Paid
embeddings are excluded from CI: regression tests use deterministic candidate/embedding
fixtures, while required integration tests exercise real PostgreSQL and Qdrant through the
API, including all three modes, filters, stale points and provenance.

## K7 management and test UI

Open `http://localhost:8090/knowledge` (or `/`, which redirects there). Connect using one
of the bearer credentials configured in `KNOWLEDGE_AUTH_TOKENS`. The UI works directly
against the standalone knowledge API; SecondContext is not involved. The credential is
kept only in this tab's JavaScript memory. Disconnect or reload clears it, aborts outstanding
requests and removes displayed workspace data. No authentication cookies, browser storage,
URL tokens, server sessions or new identity provider are introduced. The public HTML shell
contains no owner data or credentials; every data request/action still authenticates through
the existing bearer boundary. Use HTTPS when accessing the service beyond localhost.

The page uses FastAPI's [Jinja2 template support](https://fastapi.tiangolo.com/advanced/templates/)
and bundled plain JavaScript/CSS. There is no Node build, CDN, external font or runtime UI
dependency beyond Jinja2. The existing wheel/Docker build includes templates and assets.
The UI escapes imported data through text nodes, permits only HTTP/HTTPS provenance links,
and applies a restrictive Content Security Policy, no-referrer and no-store headers.
Source/API responses are also marked no-store. The API and optional Compose profile remain
compatible with existing consumers; this milestone requires no database migration.

The library shows source type/detected format, ingestion status, canonical document/chunk
counts, last successful ingestion time and latest error. Source detail polls every 2.5 seconds
while displayed jobs are active; background tabs pause polling. It displays the latest ten
jobs with stages, counters and parser/fetch/indexing errors. Website ingestion supports page,
same-path and same-host scope with a server-bounded page limit. File ingestion supports the
existing UTF-8/textual and PDF/DOCX formats, actual upload progress, format override and stable
API error codes. Paste ingestion supports an optional name, a large textarea, format override
and the detected format after parsing. All forms retain input when submission fails. Cancel
closes the add dialog; it does not cancel an already submitted ingestion job.

Sources paginate 50 at a time; documents/chunks paginate 20 at a time. Inspect a document to
expand chunk text, section/page location, canonical IDs and provenance metadata. Refresh
reuses active work or reparses; reindex queues only the projection step. With indexing disabled,
the UI can still ingest and inspect text/documents/chunks paused at `indexing`, and search
already-ready evidence. New searchable evidence needs the configured embedding/Qdrant backends.

Test retrieval accepts a query, optional source filter, result limit, hybrid/dense/sparse mode
and a score-debug toggle. The filter offers sources on the current library page and the
selected source, retaining an existing filter during page changes. Each ranked result shows
its score, title, heading/page range, URI, preview, expandable full text/IDs and optional score
components. Inspect source opens the matching canonical detail. Backend/validation errors are
shown explicitly; empty results explain that indexing must finish first.

### Confirmed deletion

The UI requires a confirmation dialog before calling `DELETE /v1/sources/{id}`. Deletion takes
the same owner projection lock and source row lock as existing writers, deletes only points
matching knowledge kind + authenticated owner + source ID with acknowledged writes, then
commits canonical source deletion. Existing FK cascades remove documents, chunks and jobs
(no job archive/retention store in this MVP). It cleans the configured collection and a prior
collection recorded on the source at the currently configured Qdrant endpoint, even when
indexing is disabled. It never creates a missing collection and requires no embeddings.

The API returns `204` for completion or an absent source, including a source belonging to
another owner, so retries are idempotent without exposing its existence. An absent collection
is already clean. Backend/acknowledgement failure returns `503 deletion_unavailable` and keeps
canonical data for retry. If a database commit fails after points were removed, retry deletion
or reindex the surviving source; refreshing/rebuilding can regenerate projections from
Postgres. Locks can time out while an ingestion worker holds the source; retry after that
attempt completes. A fresh search cannot return a canonically deleted source. Evidence from
an earlier response remains an earlier snapshot.

Canonical deletion waits for
search cleanup, so backend outages defer deletion. Moving Qdrant endpoints needs cleanup of
the old endpoint using its configuration; no endpoint history/outbox/tombstone migration is
added here. Jobs are retained while their source exists, and deleted by the same canonical
FK cascade; there is no separate archive or automatic retention expiry.

### Browser smoke test

`tests/ui_smoke.py` is an opt-in Playwright test of a running service. It uses only HTTP/browser
interfaces, uniquely names/captures its fixture sources and deletes only those IDs in a final
cleanup, including when assertions fail. It covers invalid/valid authentication, literal
hostile imported text, paste/structured ingestion, PDF/DOCX uploads, static website ingestion,
polling, chunks, hybrid/debug search, refresh/reindex, confirmation/cancel, mobile layout and
credential/data clearing. Live indexing/search use your configured provider and may incur
embedding charges. Set `KNOWLEDGE_TOKEN` privately to a configured credential, then run:

```bash
uv run --with playwright playwright install chromium
uv run --with playwright python tests/ui_smoke.py --url http://localhost:8090
# Or use an already installed browser:
uv run --with playwright python tests/ui_smoke.py --browser-path /usr/bin/chromium
```

`--website` overrides the public example URL; `--screenshot` optionally saves the displayed
workspace. Browser tooling is temporary and is not a production dependency. The regular
suite validates the credential-free shell/headers, canonical summaries, owner isolation,
writer locking, deletion retries/cascades and real Qdrant cleanup without paid inference.

## K8 lifecycle and operations

### Refresh and retained inputs

`POST /v1/sources/{id}/refresh` queues a new attempt after a finished job, or returns the
outstanding job. Text/file refresh reads the bytes already stored in Postgres; it does not
read a local filename or replace an upload. To ingest a replacement file/text through the
current API, create a new source and delete the old one after checking the replacement.
Website refresh always refetches and parses the configured URL/crawl scope.

Retained text/file parsing is reused when the input hash, detected format, name, parser
configuration, cache version and parser/resource limits match the previous parse. A changed
limit forces validation again. Document block/recipe fingerprints let chunking retain stored
rows for unchanged documents; changes deterministically regenerate only affected documents.
Unchanged semantic chunks retain their UUIDs even within a changed document. Existing K1–K7
documents acquire these cache markers during their next parse/chunk attempt; no migration
is required. Parser behavior changes must bump the input fingerprint's version.

For websites, document identity is the observed final URL. An exhausted frontier within the
configured scope/depth, with no skipped pages, page limit or discovery truncation, is
authoritative: previous documents absent from that crawl are deleted with their chunks.
Confirmed linked 404/410 responses also remove their previous URL documents, including
during an otherwise partial crawl. A failed seed still fails the job and retains data.
HTTP errors, robots denial and discovery/page caps retain unvisited documents. HTML link
discovery hitting its cap conservatively marks the frontier incomplete. Inspect
`metadata_json.crawl` for `frontier_complete`, `missing_pages`, `absence_cleanup`, limit
flags and `canonical_documents`. Job document counters describe pages processed in that
attempt; chunk counts include retained canonical pages. Website source hashes cover the
retained canonical URL/content-hash set, not just the latest partial crawl.
The retained canonical website set is also bounded by `KNOWLEDGE_WEB_MAX_PAGES`; repeated
partial refreshes cannot accumulate unlimited old/new pages. Exceeding that bound fails
with `crawl_limit_exceeded` before document changes, retaining the previous snapshot.
Retry with a complete crawl (or deliberately increase the validated server bound).

Projection replay still embeds/upserts every retained chunk, even on an unchanged refresh.
This deliberately repairs externally missing points rather than trusting an old `ready`
marker. All current writes must succeed before stale generations are deleted. Failed
projection attempts keep canonical chunks for `/reindex`; a database rollback after external
success leaves the durable job replayable. Search suppresses non-ready or outdated sources.
If parsing committed newer documents but chunking failed, reindex returns
`409 canonical_chunks_outdated`; correct the failing limit/configuration and refresh
to finish chunking before publishing those documents. A later parsing failure does not
clear this guard. Sources record `metadata_json.chunks_current` after parser/chunker commits.
Search validates canonical identity/hash/generation. The previous evidence can be temporarily
unavailable during refresh/failure. Equal backend scores sort by point ID before rank fusion,
so rebuilding does not reorder ties within the returned candidate set. Approximate retrieval
and ties across the bounded candidate cutoff can still vary in a larger corpus.

### Metrics

`GET /v1/metrics` uses the same bearer credential and returns only that owner's aggregates:

- canonical source/document/chunk counts;
- jobs by status and failures by stage (`fetching`, `parsing`, `chunking`, `indexing`);
- completed ingestion-attempt latency: count, seconds sum and maximum, from first work
  through completion, including inter-stage waits, excluding initial queue wait;
- search requests/failures, seconds sum and cumulative latency buckets per retrieval mode.

Job metrics come from retained Postgres rows and survive restarts; deleting a source deletes
its jobs and removes them from these aggregates. They are operational summaries, not an audit
archive. Search metrics are bounded, locked counters for this API process, reset on restart,
and count authenticated searches that passed request-schema validation (including runtime
validation/backend failures). With multiple API workers, collect each process separately.
No query, token, URI, source UUID or exception text becomes a metric label. There is no new
monitoring dependency or public metrics endpoint. JSON can be polled by internal tooling.

### Backup and restore

Back up the dedicated **Postgres database**, including original inputs, semantic documents,
chunks, jobs, candidate audit records, recipe manifests and `alembic_version`. Qdrant contains a disposable projection
and does not need to be the authoritative backup. Keep the service `.env`/owner credentials
and embedding configuration separately in your normal secret backup. A database dump does
not provision cluster roles/passwords. Recreate the `knowledge_bootstrap` role on a new
cluster before restoring. Backups contain private reference content and retained uploads;
store them outside the repository with restricted access and your normal retention policy.

Using the existing Postgres container from the repository root, save a custom-format dump
to a private destination (substitute your backup path):

```bash
umask 077
docker compose exec -T postgres sh -c \
  'exec pg_dump -U "$POSTGRES_USER" --format=custom --no-acl knowledge_bootstrap' \
  > /secure/backups/knowledge.dump
```

`pg_dump` supports a consistent database snapshot during normal use. For a restore drill,
create an isolated database and restore there; use no destructive cleanup flags:

```bash
docker compose exec -T postgres sh -c \
  'exec createdb -U "$POSTGRES_USER" -O knowledge_bootstrap knowledge_bootstrap_restore_integration'
docker compose exec -T postgres sh -c \
  'exec pg_restore -U "$POSTGRES_USER" --role=knowledge_bootstrap --no-owner --no-acl --exit-on-error -d knowledge_bootstrap_restore_integration' \
  < /secure/backups/knowledge.dump
```

See the PostgreSQL 16 [pg_dump](https://www.postgresql.org/docs/16/app-pgdump.html) and
[pg_restore](https://www.postgresql.org/docs/16/app-pgrestore.html) documentation for archive
and restore options. Check counts, document/chunk hashes, ownership and migration revision
in the restored database. Apply newer migrations if needed. For an actual recovery, stop
only the optional knowledge service while switching its private database configuration;
SecondContext continues running. Keep the previous database until recovery is verified.

Rebuild with the restored database and a **fresh Qdrant collection**, keeping the same
embedding model/dimensions/lexical recipe. Run `rebuild-index --owner OWNER` for every
configured owner using the operator commands above. The owner value and bearer token must
match your saved configuration. Rebuild reads canonical chunks, not websites or original
files; active parse/chunk jobs must finish before reindexing them. After readiness/search
checks, start the optional service. Incomplete jobs resume from their durable stages; failed
jobs require refresh or reindex. A fresh collection avoids accidentally serving projections
from a different point in time and permits rollback to the previous configuration.

### Evaluation and hardening

`tests/test_lifecycle_qdrant.py` ingests a two-page handbook through the website parser into
real Postgres/Qdrant, checks HTTP retrieval/provenance in all three modes, replaces a page,
injects failure after an acknowledged index batch, retries without parsing, verifies stale
points disappear, removes/rebuilds the isolated collection and compares six query/mode
rankings. It then deletes the source twice and verifies canonical/jobs/projection cleanup
and empty retrieval. Fixtures and the collection are isolated and cleaned automatically.

Run from the service directory with a dedicated database ending in `_test`/`_integration`:

```bash
KNOWLEDGE_TEST_QDRANT_URL=http://127.0.0.1:6333 \
  uv run pytest --require-postgres --require-qdrant
# Set KNOWLEDGE_TEST_DATABASE_URL privately before running.
# Optionally save a report for the lifecycle case:
KNOWLEDGE_TEST_QDRANT_URL=http://127.0.0.1:6333 \
  KNOWLEDGE_LIFECYCLE_REPORT=benchmarks/lifecycle-results.json \
  uv run pytest --require-postgres --require-qdrant tests/test_lifecycle_qdrant.py
```

The recorded [lifecycle report](benchmarks/lifecycle-results.json) uses deterministic test
embeddings and a versioned HTML fixture, so it measures lifecycle consistency, not semantic
retrieval quality or live website behavior. The K6 corpus/evaluator/results above remain the
separate real-embedding retrieval benchmark. K8 also runs the existing abuse regressions:
oversized/streamed text; JSON/YAML depth/alias expansion; DOCX ZIP/XML bombs; malformed and
oversized PDF streams; hostile/oversized HTML; actual slow sockets and redirect-to-metadata;
redirect/frontier/page/depth bounds; and cross-owner API/enumeration/projection isolation.
No OCR, browser rendering, scheduled refresh, job archive, endpoint-migration outbox or
SecondContext adapter is introduced.


## Optional derived-knowledge bridge (K10)

Extraction is **off by default** and runs only when an authenticated caller explicitly
requests it. Ingestion, search and SecondContext response generation never invoke it.
SecondContext still runs without the Python service, and the Python service still runs
without an extraction endpoint/key. K9 retrieval and its live answer evaluation preceded K10;
this feature makes no claim that extraction improves retrieval quality.

Enable extraction in the ignored service `.env`, independently of embedding configuration:

```dotenv
KNOWLEDGE_EXTRACTION_ENABLED=true
KNOWLEDGE_EXTRACTION_BASE_URL=https://api.openai.com/v1
KNOWLEDGE_EXTRACTION_API_KEY=your-private-key
KNOWLEDGE_EXTRACTION_MODEL=gpt-4.1-mini
KNOWLEDGE_EXTRACTION_TIMEOUT_SECONDS=30
KNOWLEDGE_EXTRACTION_MAX_CHUNKS=20
KNOWLEDGE_EXTRACTION_MAX_INPUT_BYTES=131072
```

The model must support the OpenAI-compatible `POST /chat/completions` API, JSON object
response format and `max_completion_tokens`. Sampling settings use the model defaults;
no unsupported temperature override or legacy `max_tokens` parameter is sent. See the
[official OpenAI chat API contract](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create). Model and endpoint selection are explicit;
credentials are never inherited automatically from SecondContext or embedding settings.
Restart the optional service after changing configuration. Apply migration
`0004_derived_candidates` before starting the updated service.

### Explicit source/document opt-in

```bash
# All current chunks of this ready source:
curl -X POST "http://localhost:8090/v1/sources/$SOURCE_ID/extract" \
  -H "Authorization: Bearer $KNOWLEDGE_TOKEN" \
  -H 'Content-Type: application/json' -d '{}'

# Only the specified documents; foreign/missing documents produce 404:
curl -X POST "http://localhost:8090/v1/sources/$SOURCE_ID/extract" \
  -H "Authorization: Bearer $KNOWLEDGE_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"document_ids":["00000000-0000-0000-0000-000000000001"]}'
```

Use `/docs` for interactive authenticated calls. The source must be `ready` with current
canonical chunks. This synchronous MVP has no automatic extraction worker, schedule or
per-source background opt-in flag. A refresh retracts invalid evidence immediately; explicitly
call extraction again once it is ready. Sources/documents never requested remain untouched.

Requests select at most 50 document IDs. The server defaults to at most 20 chunks and 128 KiB
of serialized evidence per operation, rejecting oversized scopes with `413` before model IO;
choose fewer/smaller documents rather than silently extracting a prefix. Each chunk yields
at most 20 candidates, each with a statement of at most 2,000 characters and an exact quote
of at most 4,000 characters. Model completion is limited to 4,096 tokens (including any reasoning tokens) and 256 KiB per
response. The 30-second elapsed deadline is checked before each request and between response
segments; each network operation has at most a 10-second timeout (or remaining time).
An in-progress network operation can extend elapsed time by up to that timeout. Configuration
allows an elapsed budget up to 60 seconds. Redirects, proxy inheritance, compressed responses,
truncated output and automatic retries are disabled. Errors expose stable codes, never raw
model output, source text or credentials.

### Candidate contract and consumer policy

The response contains `extraction_id`, `chunks_processed`, the extraction recipe and
`candidates`. Five kinds are supported: `entity`, `person`, `topic`, `claim`, `relationship`.
Claims and relationships require `subject`, `predicate`, `object` strings. All candidates
contain a `statement`, canonical source/document/chunk UUIDs, `evidence_json` and
`extraction_json`. Evidence includes an exact source quote, a retained chunk-text snapshot,
content hashes, title, URI, section and page information. Extraction records the configured
and reported model, endpoint, version, prompt/schema hashes and operation UUID.

Every row is explicitly `evidence_kind: documentary_claim` and
`promotion_policy: consumer_decides`, with `status: active` or `retracted`. These labels also
apply to mentions: a person mentioned in a handbook is not an episodic observation or a
personality estimate. The prompt forbids such inference; output validation enforces a strict
schema and an exact nonblank quote. Quote membership does **not** prove semantic entailment
or model accuracy. These are reviewable candidates, not accepted facts.

The service never accesses SecondContext tables or memory collections. No candidate is
automatically added to people, topics, beliefs, person observations or graph edges. Consumers
must choose an explicit semantic promotion rule, retain candidate/chunk evidence IDs and
handle retractions. Contradictory sources remain independent assertions with separate
provenance; there is no cross-source identity merge, truth arbitration or contradiction
classifier. SecondContext’s response prompt now also preserves disagreements between source
material and episodic observations instead of silently choosing or merging them.

### Polling, refresh and audit retention

```bash
curl "http://localhost:8090/v1/candidates?status=all&limit=100&offset=0" \
  -H "Authorization: Bearer $KNOWLEDGE_TOKEN"
curl "http://localhost:8090/v1/candidates/$CANDIDATE_ID" \
  -H "Authorization: Bearer $KNOWLEDGE_TOKEN"
```

Lists default to active rows. Filter by `source_id`, `document_id`, or
`status=active|retracted|all`; pagination defaults to 50 and permits 100 rows per page.
Owner identity comes exclusively from the bearer token, including after source deletion.
For this MVP, consumers periodically reconcile a **complete paginated owner snapshot**,
including retracted rows, against their retained IDs. Repeat polling while writers are active;
offset pagination is not a transaction snapshot or exactly-once event stream. A previously
known ID that is absent/404 has been purged and must also be removed from consumer projections.
No webhook, broker or incremental timestamp cursor is required.

Postgres triggers atomically retract candidates on document content/provenance changes,
chunk deletion/update and source/document deletion, including cascading or direct SQL writes.
Unchanged refreshes retain candidates when canonical chunks/provenance are reused. A change
is visible as a retraction at parsing time, even before new chunks finish indexing; parsing
failure before a canonical change retains the previous candidates. Retraction rolls back
with a failed canonical mutation. The extractor releases its read transaction during model
IO, then locks/revalidates the complete selected snapshot before committing; concurrent
refresh/deletion yields `409 evidence_changed`. No partial output is committed on failure.

A successful recomputation replaces active candidates only for the selected documents,
including when the new result is empty. Identical assertions under the same configured
recipe/chunk reuse their UUID and update operation/model metadata; removed assertions remain
retracted with reason `recomputed`. Failed extraction retains the prior result. Calls may be
retried manually, but every call performs model IO and may incur cost; there is no durable
extraction-job history or promise of deterministic model output. The operation UUID groups
a successful result; zero-result operations are returned rather than stored as separate runs.

Source deletion still removes canonical content/jobs and Qdrant points. **Candidate audit
records deliberately survive** with their last evidence snapshot, statement and retraction
reason, so consumers can retract promoted assertions and reviewers can inspect old claims.
This is additional retained source content: include it in backup/retention policy. To remove
it permanently, list the source's candidates with `status=all` and purge each captured ID:

```bash
curl -X DELETE "http://localhost:8090/v1/candidates/$CANDIDATE_ID" \
  -H "Authorization: Bearer $KNOWLEDGE_TOKEN"
```

Purging is owner-scoped, idempotent (`204` even if absent/foreign) and irreversible; it removes
that candidate's snapshot. A later explicit extraction can recreate a purged active assertion
while its source exists. There is no automatic audit-expiry schedule in this MVP.

### Validation

Unit tests exercise all five candidate kinds, exact-quote/schema enforcement, private errors,
backend timeout/deadline, oversized/unfinished output, redirects and default-off operation.
Postgres integration tests use real migrations and cover ownership, UUID reuse, document
opt-in, empty/partial/failed recomputation, concurrent evidence changes, unchanged/changed
refresh, transactional retraction/rollback, deletion and purge, and contradictory sources.
The Go consumer regression preserves contradictory documentary and episodic inputs plus
provenance and checks the explicit non-promotion/contradiction policy. This verifies the
assembled prompt contract, not a general guarantee about LLM answers or extraction quality.

Deployment validation also exercised the real configured embedding/chat backends through
the HTTP API: two conflicting fixture sources were extracted, one was refreshed to a changed
claim and explicitly recomputed, and source deletion retracted its candidates before audit
purge. Quotes and canonical UUIDs matched, memory-item IDs stayed unchanged, health/readiness
passed, and all captured sources, search points and audit snapshots were removed. This is a
small functional demonstration rather than an extraction accuracy benchmark.
