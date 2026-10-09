# Knowledge base

The knowledge base is an optional, independently deployable service for durable
reference material. It ingests documents/websites and returns ranked passages
with inspectable provenance through `POST /v1/search`. The Go gateway can use it,
but neither application requires the other. The implementation lives in
[`services/knowledge-bootstrap`](../services/knowledge-bootstrap/README.md).

The completed functionality covers the original K1–K10 milestones: canonical
storage/jobs, textual and binary ingestion, safe static crawling, chunking/indexing,
hybrid search, management UI, recovery/evaluation, the Go HTTP adapter and optional
documentary candidates. This guide records current behavior; [TODO.md](../TODO.md)
retains future work. Decision rationale lives in
[ADR 0003](adr/0003-standalone-canonical-knowledge.md),
[ADR 0004](adr/0004-http-knowledge-adapter.md) and
[ADR 0005](adr/0005-documentary-candidates.md).

## Deployment and ownership

FastAPI serves the API and Jinja2 UI on port 8090. SQLAlchemy/psycopg and Alembic
use a dedicated Postgres database/role. Qdrant uses a dedicated `knowledge_chunks`
collection. Existing infrastructure can be shared with SecondContext, while tables,
credentials and index recipes remain independent. Settings use `KNOWLEDGE_` in
the service's own environment; the root Go environment is not inherited.

`KNOWLEDGE_AUTH_TOKENS` is a JSON map of opaque owner IDs to distinct bearer tokens
of at least 16 characters without whitespace. Every `/v1` call authenticates;
there is no caller-selectable owner. Foreign resource lookups appear missing.
Composite foreign keys enforce owner/provenance consistency for canonical rows.
Candidate snapshots survive evidence deletion and are scoped by the candidate APIs.

See [operations](operations.md#knowledge-service) for provisioning, migrations,
refresh/reindex, recovery, backups and deletion. The
[service environment template](../services/knowledge-bootstrap/.env.example)
lists validated settings. `/docs` exposes the actual request/response schemas.

## Canonical data and jobs

| Postgres table | Responsibility |
| --- | --- |
| `knowledge_sources` | Owner-managed URL/file/text origin, retained input, configuration, status, hashes and lifecycle metadata |
| `knowledge_documents` | Logical document/page with stable URI, title, normalized text, semantic blocks and hash |
| `knowledge_chunks` | Retrieval text, deterministic UUID/hash, ordinal, tokens, heading/page/structure provenance and recipe |
| `knowledge_ingestion_jobs` | Durable attempt, stage/status, progress counters, timestamps and sanitized errors |
| `knowledge_index_configurations` | Collection manifest pinning embedding and lexical configuration |
| `knowledge_candidates` | Optional documentary assertions and retained evidence/extraction audit snapshots |

A pasted/uploaded document normally produces one document; a crawl produces one
per observed final URL. Text is retained after UTF-8 normalization, PDF/DOCX bytes
are retained privately in Postgres, and raw HTML is discarded after parsing.
Original upload names are provenance and never become filesystem paths.

Source creation commits a source/job together and returns `202` with both IDs.
Optional `Idempotency-Key` is owner-scoped: identical input reuses the original
source/job; conflicting reuse returns `409 idempotency_conflict`. One source has
at most one active attempt. Refresh/reindex reuse outstanding jobs; terminal
attempts remain immutable and later attempts get new job IDs.

```text
text/file: pending -> parsing -> chunking -> indexing -> ready
URL:       pending -> fetching -> parsing -> chunking -> indexing -> ready
active stages can fail; failed jobs retain their stage and error
```

The in-process polling worker uses durable row locks and `SKIP LOCKED`. Source
locks precede job locks; projection writers additionally serialize by owner.
Crashes/database rollback leave work retryable. Counters are monotonic within
an attempt; last successful ingestion time changes only on success. The setting
`KNOWLEDGE_TEXT_WORKER_ENABLED` controls text, binary and URL work despite its name.
Disabling indexing leaves canonical chunks inspectable at `indexing`; enabling it
resumes paused jobs. Failed attempts require an explicit retry.

## Parsing and crawler safety

| Input | Preserved structure and current limits |
| --- | --- |
| TXT / pasted text | UTF-8 text and paragraphs; conservative auto detection, explicit override for ambiguous input |
| Markdown | Heading ancestry, paragraphs, lists, quotes and code; no execution |
| JSON / YAML | Deterministic sorted readable JSON, typed scalars/arrays and escaped JSON Pointer paths; duplicate keys and unsafe structures rejected |
| PDF | Digital text with one-based physical page ranges and low-text-page metadata; encrypted/scanned-only PDFs fail explicitly |
| DOCX | Body order, heading ancestry, paragraphs, lists, hyperlink text and readable tables; no reliable rendered pagination |
| Website | Static HTML/XHTML main-content heuristic, headings/paragraphs/lists/code/tables, observed final URL and canonical hint metadata |

No parsing path uses an LLM to invent normalized prose. Text defaults to 512 KiB,
PDF/DOCX bytes to 8 MiB, whole requests to 10 MiB and normalized output to 2 MiB.
Structured depth/nodes/aliases, PDF pages/streams and ZIP/XML expansion are bounded.
Binary parsers run in disposable Linux/POSIX processes with wall/CPU/memory/file
limits and no database/auth credentials. Reading order and semantic extraction
are approximate for complex visual layouts. See the
[parser reference](../services/knowledge-bootstrap/README.md#parsing-contract-and-limits)
for exact settings and error codes.

Website fetching is a security boundary:

- Permit public HTTP:80 and HTTPS:443 only, without URL credentials, inherited
  proxies, cookies or custom authentication.
- Reject the whole A/AAAA answer set if any address is non-public or a denied
  metadata/platform/tunnel target. Connect to a validated numeric address,
  verify the peer, and use the original TLS hostname. Revalidate redirects.
- Apply the same transport to robots requests. Check robots before pages and
  redirects. Missing robots (404/410) permits access; other failures deny it.
- Crawl sequentially with bounded page attempts, depth, frontier, links, bytes,
  redirects and total time. Delay defaults to at least one second per hostname
  within a crawl, honoring larger robots delays. Replicas do not share delay state.
- Reject compressed/oversized responses and enforce an absolute request deadline
  across DNS, connections and reads. Run the crawler in a bounded child process.

Default source scope is one page. `path` follows the seed path and descendants
at slash boundaries on the exact hostname; `host` follows that exact hostname.
Crawl modes default to ten pages/depth two within server maxima of twenty/three.
Single-page mode permits checked public cross-host redirects; crawl redirects
stay within scope. Canonical HTML hints are metadata, not fetch authority or
document identity. JavaScript rendering, OCR and remote asset loading are absent.
Full rules are in the [crawler reference](../services/knowledge-bootstrap/README.md#website-ingestion-k4).

## Chunking and index consistency

Every parser emits a common semantic document/block representation before
chunking. The chunker reads stored blocks, preserving headings, structural paths
and page ranges. Defaults are 600 target tokens and 1,200 hard maximum with fixed
`cl100k_base` counting, plus a 16,384-character ceiling. Paragraphs pack within
sections; code/tables/lists/values stay intact unless the hard limit requires
splitting. Only oversized unstructured paragraphs receive up to 40 suffix tokens
of overlap. Unicode splitting is lossless.

SHA-256 covers chunk text and heading/page location. UUIDv5 includes document ID,
hash and duplicate occurrence, so unchanged semantic chunks retain identity.
Stored chunk metadata records block indexes/types, paths, overlap and the versioned
recipe. Changing chunking settings needs refresh, not projection-only replay.

Qdrant stores named Cosine `dense` and IDF-enabled `sparse` vectors. Sparse
encoding uses Unicode case-folded words, log term frequency and SHA-256 32-bit
hashes. Hash collisions are possible; there is no learned sparse model or stemming.
The collection manifest pins endpoint/model/dimensions and lexical recipe before
writes. A new embedding recipe needs a fresh collection and rebuild for each owner.

Write order is canonical documents, chunks/indexing state, recipe manifest,
acknowledged Qdrant upserts and stale-generation cleanup, then `ready`. Failed
projection attempts retain canonical chunks. Replaying stable point IDs repairs
partial writes and missing points without refetching. Search validates canonical
owner/IDs/hash/generation/collection/recipe/readiness after retrieving candidates;
it never trusts backend passage text or repairs indexes during a read.
Cross-store batches are not atomic, so refresh/failure may temporarily hide evidence.

## Search API and UI

```bash
curl http://localhost:8090/v1/search \
  -H "Authorization: Bearer $KNOWLEDGE_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"query":"How do we roll back deployment?","limit":5,"mode":"hybrid","debug":true,"filters":{"source_ids":[],"document_ids":[],"formats":[]}}'
```

Modes are `hybrid` (default), `dense` and `sparse`. Sparse-only makes no embedding
request. Query bounds are 2,048 characters and 1,024 tokens; result limit defaults
to five with server maximum twenty. Filters use OR within a field and AND across
fields. Empty lists impose no restriction. Nonempty tags are not implemented.
Candidates default to 100 per mode, capped at 200; validation/deduplication can
return fewer results than requested.

Hybrid ranking uses equal reciprocal rank fusion (`k=60`). Reranking uses 82%
fusion, 12% title coverage and 6% heading coverage; numeric identifiers move 10
percentage points from fusion to exact term coverage. Selection softly discounts
repeated documents/sections and deduplicates passages at 85% word-five-gram
Jaccard similarity. Scores are ranking heuristics in [0,1], not confidence.
There is no recency decay, implicit source trust or general contradiction solver.

Results contain canonical source/document/chunk UUIDs, text, score, title,
heading ancestry, URI/source URI, format and page range where available. Debug
output includes ranks/raw backend scores, fusion, coverage and diversity effects.
No ready sources means empty evidence without backend calls; failures against
existing ready evidence produce sanitized 503s. See the
[search contract](../services/knowledge-bootstrap/README.md#k6-hybrid-retrieval-api).

The HTTP surface includes source creation/upload/list/detail/delete,
refresh/reindex, source documents/jobs, document/chunk and job inspection, search,
owner metrics and optional candidate extraction/list/detail/purge. Exact schemas
and pagination are available at `/docs` and in the
[API reference](../services/knowledge-bootstrap/README.md#ownership-and-api).

Open `/knowledge` for source management, job progress, chunk inspection and test
retrieval. The Jinja2 shell has no owner data; all data/actions authenticate through
the API. Tokens live only in the tab's JavaScript memory and clear on reload or
disconnect. Assets are bundled, imported data is escaped, and CSP/no-store/
no-referrer headers apply. Source deletion requires UI confirmation. The UI also
works when SecondContext is absent.

## SecondContext integration

The disabled-by-default Go `KnowledgeProvider` calls only `/v1/search`. Configure
exact subject-to-owner-token mappings; the authenticated resolved subject selects
the credential. Memory and reference retrieval remain independent, with separate
disable controls and labeled prompt sections. Response/debug packets retain the
actual bounded evidence and provenance. Gateway retrieval does not extract
candidates or write cognitive state. See [adapter setup](../README.md#reference-knowledge-adapter-k9)
and [context budgets](architecture.md#knowledge-ingestion-and-retrieval).

## Optional derived knowledge

`KNOWLEDGE_EXTRACTION_ENABLED=false` by default. Explicit
`POST /v1/sources/{id}/extract` selects all current chunks or document IDs of a
ready source. It is synchronous and separately configured; no ingestion/search
or gateway response automatically calls it. Defaults bound extraction to twenty
chunks, 128 KiB evidence and a thirty-second elapsed budget with bounded network
operations. Oversized scopes fail before model IO instead of extracting a prefix.

Strict candidates use `entity`, `person`, `topic`, `claim` or `relationship`, with
statement, exact quote, canonical IDs, retained chunk/provenance snapshots and
model/recipe metadata. Claims/relationships require subject/predicate/object.
All are labeled `documentary_claim` and `consumer_decides`. Exact quotes prove
membership, not semantic accuracy. Contradictory sources remain separate claims.

After model IO the extractor locks/revalidates selected evidence; concurrent
changes yield `409 evidence_changed` without partial commits. Recompute replaces
only selected documents' active results, preserving stable IDs for unchanged
assertions and retracting removed ones. Failed extraction retains prior output.
Canonical mutation triggers retract evidence transactionally, including on deletion.
Refresh requires explicit extraction again once the source is ready.

Candidate audits deliberately survive source deletion. Poll `/v1/candidates`
with `status=all`, paginate the full owner snapshot and reconcile retracted or
purged IDs in consumer projections. Offset polling is not a snapshot/event stream.
Explicit candidate DELETE permanently removes the retained audit. There is no
automatic promotion, recomputation, extraction history or audit expiry.
See [retention operations](operations.md#deletion-and-candidate-audit-retention)
and the [full candidate contract](../services/knowledge-bootstrap/README.md#optional-derived-knowledge-bridge-k10).

## Verification and limits

Independent unit/integration lanes cover parser fixtures and abuse bounds,
ownership, crawler transport, stable chunk identity, real Postgres/Qdrant failure
and rebuild/delete behavior, HTTP search, UI and candidate retraction. CI uses
deterministic inference fixtures; paid models are not required. Commands and
environment requirements are in [operations](operations.md#verification-and-release).

The [retrieval corpus/results](../services/knowledge-bootstrap/benchmarks/results.json)
record ten labeled queries over sixteen documents with real embeddings. Hybrid
and dense both achieved mean precision@3 0.4333; dense had better top-one precision
on this small corpus. This demonstrates usable retrieval rather than universal
hybrid improvement. The separate [lifecycle report](../services/knowledge-bootstrap/benchmarks/lifecycle-results.json)
uses deterministic embeddings to measure recovery/provenance, not semantic quality.

OCR, browser rendering, scheduled refresh, additional formats, source-priority
controls, sharing/permissions, external blob storage, incremental document diffs,
custom rerankers and richer extraction/contradiction analysis remain deferred in
[TODO.md](../TODO.md#knowledge-base-follow-ups).
