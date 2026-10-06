"""Canonical-first chunking and durable, source-serialized projection recovery."""

import hashlib
from dataclasses import asdict
from uuid import uuid5

from sqlalchemy import delete, select, text
from sqlalchemy.dialects.postgresql import insert

from knowledge_bootstrap.chunking import chunk_blocks, recipe, token_count
from knowledge_bootstrap.index import (
    IndexError,
    SearchIndex,
    index_recipe,
    owned_filter,
    sparse_vector,
)
from knowledge_bootstrap.models import (
    Chunk,
    Document,
    IndexConfiguration,
    IngestionJob,
    Source,
    Stage,
)
from knowledge_bootstrap.parsers import ParseError
from knowledge_bootstrap.representation import Block
from knowledge_bootstrap.schemas import JobTransition
from knowledge_bootstrap.service import ServiceError, apply_transition


def lock_owner(session, owner, *, wait=True):
    # All projection/rebuild writers take this before source locks. No shared Go locks/tables.
    key = int.from_bytes(
        hashlib.sha256(("knowledge-index:" + owner).encode()).digest()[:8], "big", signed=True
    )
    function = "pg_advisory_xact_lock" if wait else "pg_try_advisory_xact_lock"
    return session.scalar(text(f"SELECT {function}(:key)"), {"key": key})


def chunk_source(session, source, job, settings):
    documents = session.scalars(
        select(Document)
        .where(
            Document.owner_id == source.owner_id,
            Document.source_id == source.id,
        )
        .order_by(Document.uri)
    ).all()
    planned = []
    try:
        for doc in documents:
            blocks = [Block(**b) for b in doc.metadata_json["blocks"]]
            planned.append((doc, chunk_blocks(blocks, settings)))
        total = sum(len(specs) for _, specs in planned)
        if not documents or total > settings.max_chunks_per_source:
            raise ParseError(
                "chunk_limit_exceeded", "Source has no documents or exceeds the chunk limit"
            )
    except (KeyError, TypeError, ValueError):
        raise ParseError(
            "invalid_document_blocks", "Canonical semantic blocks are invalid"
        ) from None
    # Plan before mutation so a chunking failure preserves the previous canonical snapshot.
    session.execute(
        delete(Chunk).where(Chunk.owner_id == source.owner_id, Chunk.source_id == source.id)
    )
    for doc, specs in planned:
        occurrences = {}
        for ordinal, spec in enumerate(specs):
            occurrence = occurrences.get(spec.content_hash, 0)
            occurrences[spec.content_hash] = occurrence + 1
            identity = uuid5(doc.id, f"chunk-v1:{spec.content_hash}:{occurrence}")
            session.add(
                Chunk(
                    id=identity,
                    owner_id=source.owner_id,
                    source_id=source.id,
                    document_id=doc.id,
                    ordinal=ordinal,
                    **asdict(spec),
                )
            )
    source.metadata_json = {**source.metadata_json, "chunking": recipe(settings)}
    apply_transition(source, job, JobTransition(status=Stage.INDEXING, chunks_created=total))


def process_chunk_next(sessions, settings):
    with sessions.begin() as session:
        source = session.scalar(
            select(Source)
            .join(IngestionJob, IngestionJob.source_id == Source.id)
            .where(IngestionJob.status == Stage.CHUNKING, Source.owner_id.in_(settings.auth_tokens))
            .order_by(IngestionJob.created_at, IngestionJob.id)
            .with_for_update(of=Source, skip_locked=True)
            .limit(1)
        )
        if source is None:
            return False
        job = session.scalar(
            select(IngestionJob)
            .where(IngestionJob.source_id == source.id, IngestionJob.status == Stage.CHUNKING)
            .with_for_update()
        )
        if job is None:
            return False
        try:
            chunk_source(session, source, job, settings)
        except ParseError as exc:
            apply_transition(
                source,
                job,
                JobTransition(status=Stage.FAILED, error_code=exc.code, error_detail=exc.detail),
            )
    return True


def verify_recipe(session, settings):
    target = hashlib.sha256(
        (settings.qdrant_url + "/" + settings.qdrant_collection).encode()
    ).hexdigest()
    config = index_recipe(settings)
    session.execute(
        insert(IndexConfiguration)
        .values(target=target, config_json=config)
        .on_conflict_do_nothing(index_elements=["target"])
    )
    if session.get(IndexConfiguration, target).config_json != config:
        raise IndexError(
            "index_configuration_mismatch",
            "Embedding or lexical recipe changed; select a new collection and rebuild",
        )


def project_source(session, source, job, settings, index_factory=SearchIndex):
    rows = session.execute(
        select(Chunk, Document)
        .join(Document, Chunk.document_id == Document.id)
        .where(Chunk.owner_id == source.owner_id, Chunk.source_id == source.id)
        .order_by(Chunk.document_id, Chunk.ordinal)
    ).all()
    if not rows or len(rows) > settings.max_chunks_per_source:
        raise IndexError(
            "index_no_chunks", "Source has no canonical chunks or exceeds the chunk limit"
        )
    verify_recipe(session, settings)
    with index_factory(settings) as index:
        index.ensure_collection()
        for offset in range(0, len(rows), settings.index_batch_size):
            batch = rows[offset : offset + settings.index_batch_size]
            inputs = [
                "\n\n".join([doc.title, " > ".join(chunk.heading_path), chunk.text])
                for chunk, doc in batch
            ]
            if any(len(value) > 32768 or token_count(value) > 8192 for value in inputs):
                raise IndexError(
                    "embedding_input_too_large",
                    "Title, heading and chunk exceed the embedding limit",
                )
            vectors = index.embed(inputs)
            points = []
            for (chunk, doc), value, vector in zip(batch, inputs, vectors, strict=True):
                points.append(
                    {
                        "id": str(chunk.id),
                        "vector": {"dense": vector, "sparse": sparse_vector(value)},
                        "payload": {
                            "kind": "knowledge",
                            "owner_id": source.owner_id,
                            "source_id": str(source.id),
                            "document_id": str(doc.id),
                            "chunk_id": str(chunk.id),
                            "title": doc.title,
                            "url": doc.uri,
                            "source_uri": source.source_uri,
                            "heading_path": chunk.heading_path,
                            "page_start": chunk.page_start,
                            "page_end": chunk.page_end,
                            "format": doc.format,
                            "content_hash": chunk.content_hash,
                            "ordinal": chunk.ordinal,
                            "token_count": chunk.token_count,
                            "canonical_url": doc.metadata_json.get("canonical_url"),
                            "retrieved_at": doc.metadata_json.get("retrieved_at"),
                            "projection_generation": str(job.id),
                        },
                    }
                )
            index.upsert(points)
        # Delete only after every current chunk was acknowledged. On partial failure retain
        # the durable indexing job/snapshot; retry replays upserts and this bounded filter.
        stale = owned_filter(source.owner_id, source.id)
        stale["must_not"] = [{"key": "projection_generation", "match": {"value": str(job.id)}}]
        index.delete_filter(stale)
    source.metadata_json = {
        **source.metadata_json,
        "index": {
            "collection": settings.qdrant_collection,
            "generation": str(job.id),
            "recipe": index_recipe(settings),
        },
    }
    apply_transition(source, job, JobTransition(status=Stage.READY))


def process_index_next(sessions, settings, index_factory=SearchIndex, *, source_id=None):
    if not settings.indexing_enabled:
        return False
    # Commit the recipe before holding a source transaction. This also works with a
    # single-connection pool, and a crash after external success cannot unpin the model.
    recipe_error = None
    with sessions.begin() as manifest_session:
        candidates = select(IngestionJob.id).where(
            IngestionJob.status == Stage.INDEXING,
            IngestionJob.owner_id.in_(settings.auth_tokens),
        )
        if source_id is not None:
            candidates = candidates.where(IngestionJob.source_id == source_id)
        if manifest_session.scalar(candidates.limit(1)) is None:
            return False
        try:
            verify_recipe(manifest_session, settings)
        except IndexError as exc:
            recipe_error = exc
    # Owner-level advisory lock also protects rebuild orphan cleanup from concurrent writes.
    for owner in settings.auth_tokens:
        with sessions.begin() as session:
            if not lock_owner(session, owner, wait=False):
                continue
            query = (
                select(Source)
                .join(IngestionJob, IngestionJob.source_id == Source.id)
                .where(Source.owner_id == owner, IngestionJob.status == Stage.INDEXING)
            )
            if source_id is not None:
                query = query.where(Source.id == source_id)
            source = session.scalar(
                query.order_by(IngestionJob.created_at, IngestionJob.id)
                .with_for_update(of=Source, skip_locked=True)
                .limit(1)
            )
            if source is None:
                continue
            job = session.scalar(
                select(IngestionJob)
                .where(IngestionJob.source_id == source.id, IngestionJob.status == Stage.INDEXING)
                .with_for_update()
            )
            if job is None:
                continue
            try:
                if recipe_error is not None:
                    raise recipe_error
                project_source(session, source, job, settings, index_factory)
            except IndexError as exc:
                apply_transition(
                    source,
                    job,
                    JobTransition(
                        status=Stage.FAILED, error_code=exc.code, error_detail=exc.detail
                    ),
                )
            return True
    return False


def reindex_source(session, owner, source_id):
    """New durable indexing attempt, without refetching/reparsing/rechunking."""
    with session.begin():
        source = session.scalar(
            select(Source).where(Source.id == source_id, Source.owner_id == owner).with_for_update()
        )
        if source is None:
            raise ServiceError("not_found", "Resource not found", 404)
        active = session.scalar(
            select(IngestionJob).where(
                IngestionJob.source_id == source.id,
                IngestionJob.status.not_in((Stage.READY, Stage.FAILED)),
            )
        )
        if active is not None:
            return source, active
        chunks = session.scalars(
            select(Chunk).where(Chunk.source_id == source.id, Chunk.owner_id == owner)
        ).all()
        if not chunks:
            raise ServiceError("no_canonical_chunks", "Parse/chunk or refresh the source first")
        count = len({c.document_id for c in chunks})
        source.status = Stage.INDEXING
        job = IngestionJob(
            owner_id=owner,
            source_id=source.id,
            status=Stage.INDEXING,
            stage=Stage.INDEXING,
            documents_found=count,
            documents_processed=count,
            chunks_created=len(chunks),
        )
        session.add(job)
        session.flush()
    return source, job


def rebuild_index(sessions, settings, owner, index_factory=SearchIndex):
    """Replay all existing canonical chunks; never wipe another owner's projection."""
    with sessions() as session:
        ids = session.scalars(
            select(Source.id).where(
                Source.owner_id == owner,
                Source.id.in_(select(Chunk.source_id).where(Chunk.owner_id == owner)),
            )
        ).all()
    selected = settings.model_copy(update={"auth_tokens": {owner: settings.auth_tokens[owner]}})
    jobs = []
    for source_id in ids:
        with sessions() as session:
            _, job = reindex_source(session, owner, source_id)
            jobs.append(job.id)
        process_index_next(sessions, selected, index_factory, source_id=source_id)
    # Orphan cleanup takes the same lock as all projection writes. Canonical parsing can
    # continue; no new source can project until this committed snapshot/filter completes.
    with sessions.begin() as session:
        lock_owner(session, owner)
        verify_recipe(session, settings)
        current = session.scalars(select(Source.id).where(Source.owner_id == owner)).all()
        with index_factory(settings) as index:
            index.ensure_collection()
            orphan = owned_filter(owner)
            if current:
                orphan["must_not"] = [
                    {"key": "source_id", "match": {"any": [str(i) for i in current]}}
                ]
            index.delete_filter(orphan)
    with sessions() as session:
        return session.scalars(select(IngestionJob).where(IngestionJob.id.in_(jobs))).all()
