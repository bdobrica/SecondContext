# ADR 0003: Standalone canonical knowledge and rebuildable retrieval

Status: Accepted — recorded retrospectively 2026-10-10

## Context

Reference documents must be useful before any conversation history exists.
Documentary material has different ingestion, provenance and ranking needs from
episodic memory. The shortest usable implementation should share infrastructure
when convenient while remaining independently deployable.

## Decision

Use an optional FastAPI service with its own dependencies, configuration,
authentication, Alembic migrations, Postgres database and Qdrant collection.
Serve management and retrieval testing through Jinja2 and bundled plain
JavaScript/CSS. No Node build or consumer gateway is required.

Postgres owns sources, retained inputs, semantic documents, chunks, durable jobs
and index recipe manifests. Bearer credentials determine opaque owners; callers
cannot select an owner. Composite foreign keys preserve owner/provenance
consistency. Qdrant holds a disposable projection and never supplies canonical
passage text or provenance.

Converge deterministic parsers on one semantic block model. Bound text/structured
inputs and isolate PDF/DOCX parsing in resource-limited processes. Static web
fetching validates all DNS results, pins the validated peer, verifies TLS against
the hostname and repeats checks across redirects and robots requests. Use bounded
sequential crawling and conservative robots behavior; exclude OCR and browser
rendering from the MVP.

Chunk stored semantic blocks with heading/paragraph boundaries, exact token bounds
and limited overlap for oversized unstructured paragraphs. Hash content/location
and use deterministic UUIDs so unchanged chunks retain identity. Index named
dense and Unicode hashed log-TF sparse vectors with Qdrant IDF. Pin the projection
recipe in Postgres before remote writes. Fuse search ranks, boost title/heading
and numeric identifiers, then diversify/deduplicate without episodic recency decay.

Commit canonical documents, chunks and durable indexing state before remote
writes. Serialize projections per owner; use acknowledged UUID upserts followed
by stale-generation cleanup. Validate search candidates against canonical owner,
hash, generation, recipe and source readiness. Reindex/rebuild replay stored
chunks; refresh reparses/rechunks. Delete acknowledged source-scoped index points
before committing canonical cascades.

## Consequences

Either application runs without the other. External consumers need only HTTP
search. Index loss can be recovered from canonical chunks without original files
or websites. Shared infrastructure does not imply shared storage ownership.

Cross-store writes are not atomic; evidence may be temporarily unavailable during
refresh/failure. Qdrant outages defer canonical source deletion. Complete crawls
authorize absence cleanup, while partial crawls preserve unvisited pages. Original
inputs and normalized content require private backups. Crawling across replicas
has no shared host-delay coordinator. Embedding recipe changes need a fresh
collection and owner-by-owner rebuild; chunk recipe changes need refresh.

See [knowledge-base behavior](../knowledge-base.md) and
[recovery procedures](../operations.md#knowledge-service).
