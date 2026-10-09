"""Small UI-facing summaries and serialized source deletion; no separate UI data store."""

from sqlalchemy import func, select
from sqlalchemy.orm import load_only

from knowledge_bootstrap.index import IndexError, SearchIndex, owned_filter
from knowledge_bootstrap.models import Chunk, Document, IngestionJob, Source
from knowledge_bootstrap.pipeline import lock_owner
from knowledge_bootstrap.schemas import SourceSummaryView, SourceView
from knowledge_bootstrap.service import ServiceError


def source_summaries(session, owner, *, source_id=None, limit=50, offset=0):
    latest = (
        select(IngestionJob)
        .where(IngestionJob.source_id == Source.id, IngestionJob.owner_id == owner)
        .order_by(IngestionJob.created_at.desc(), IngestionJob.id.desc())
        .limit(1)
        .correlate(Source)
    )
    columns = [
        select(func.count(model.id))
        .where(model.source_id == Source.id, model.owner_id == owner)
        .correlate(Source)
        .scalar_subquery()
        for model in (Document, Chunk)
    ]
    columns += [
        latest.with_only_columns(field).scalar_subquery()
        for field in (IngestionJob.stage, IngestionJob.error_code, IngestionJob.error_detail)
    ]
    statement = (
        select(Source, *columns)
        .options(load_only(*(getattr(Source, field) for field in SourceView.model_fields)))
        .where(Source.owner_id == owner)
        .order_by(Source.created_at.desc(), Source.id)
        .offset(offset)
        .limit(limit)
    )
    if source_id is not None:
        statement = statement.where(Source.id == source_id)
    return [
        SourceSummaryView(
            **SourceView.model_validate(source).model_dump(),
            document_count=documents,
            chunk_count=chunks,
            latest_stage=stage,
            last_error_code=code,
            last_error_detail=detail,
        )
        for source, documents, chunks, stage, code, detail in session.execute(statement)
    ]


def delete_source(session, settings, owner, source_id, *, index_factory=None):
    """Projection first under writer locks, then canonical FK cascades. Missing = success.

    Backend failure leaves canonical data intact for retry. If the PG commit fails after
    cleanup, retry deletion or reindex the surviving source. No embeddings are required.
    """
    index_factory = index_factory or SearchIndex
    with session.begin():
        lock_owner(session, owner)
        source = session.scalar(
            select(Source).where(Source.id == source_id, Source.owner_id == owner).with_for_update()
        )
        if source is None:
            return
        try:
            collections = {settings.qdrant_collection}
            projection = source.metadata_json.get("index")
            if isinstance(projection, dict) and projection.get("collection"):
                collections.add(projection["collection"])
            # Include a previous collection on this endpoint if configuration was changed.
            for collection in sorted(collections):
                config = settings.model_copy(update={"qdrant_collection": collection})
                # Validate persisted collection names before constructing backend URLs.
                config = type(settings).model_validate(config.model_dump())
                with index_factory(
                    config, timeout_seconds=settings.search_timeout_seconds
                ) as index:
                    index.delete_filter(owned_filter(owner, source_id), allow_missing=True)
        except (IndexError, ValueError, TypeError):
            raise ServiceError(
                "deletion_unavailable",
                "Search cleanup failed; source retained. Retry deletion.",
                503,
            ) from None
        session.delete(source)
