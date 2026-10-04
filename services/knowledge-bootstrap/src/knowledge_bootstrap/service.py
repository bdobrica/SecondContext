import hashlib
import json
from datetime import UTC, datetime
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from knowledge_bootstrap.models import TERMINAL, IngestionJob, Source, SourceKind, Stage
from knowledge_bootstrap.schemas import JobTransition, SourceCreate


class ServiceError(Exception):
    def __init__(self, code: str, detail: str, status_code: int = 409):
        self.code = code
        self.detail = detail
        self.status_code = status_code
        super().__init__(detail)


def get_owned(session: Session, model, owner: str, row_id: UUID):
    row = session.scalar(select(model).where(model.id == row_id, model.owner_id == owner))
    if row is None:
        raise ServiceError("not_found", "Resource not found", 404)
    return row


def _creation_replay(session: Session, owner: str, key: str, fingerprint: str):
    source = session.scalar(
        select(Source).where(Source.owner_id == owner, Source.request_key == key)
    )
    if source is None:
        return None
    if source.request_hash != fingerprint:
        raise ServiceError(
            "idempotency_conflict", "Idempotency-Key was used for a different payload"
        )
    # Return the original job even if a later refresh has occurred.
    job = session.scalar(
        select(IngestionJob)
        .where(IngestionJob.owner_id == owner, IngestionJob.source_id == source.id)
        .order_by(IngestionJob.created_at, IngestionJob.id)
        .limit(1)
    )
    return source, job


def create_source(
    session: Session, owner: str, payload: SourceCreate, key: str | None = None
) -> tuple[Source, IngestionJob]:
    fingerprint = hashlib.sha256(
        json.dumps(payload.model_dump(mode="json"), sort_keys=True, separators=(",", ":")).encode()
    ).hexdigest()
    try:
        with session.begin():
            if key and (existing := _creation_replay(session, owner, key, fingerprint)):
                return existing
            source = Source(
                owner_id=owner,
                kind=payload.kind,
                name=payload.name,
                source_uri=payload.source_uri,
                content_type=payload.content_type,
                format=payload.format,
                config_json=payload.config_json,
                metadata_json=payload.metadata_json,
                input_text=payload.text,
                content_hash=(
                    hashlib.sha256(payload.text.encode()).hexdigest() if payload.text else None
                ),
                request_key=key,
                request_hash=fingerprint if key else None,
            )
            session.add(source)
            session.flush()
            job = IngestionJob(owner_id=owner, source_id=source.id)
            session.add(job)
            session.flush()
        return source, job
    except IntegrityError as exc:
        # A concurrent caller may have committed the same request while we were creating it.
        if key and exc.orig.diag.constraint_name == "uq_sources_request_key":
            with session.begin():
                if existing := _creation_replay(session, owner, key, fingerprint):
                    return existing
        raise


def refresh_source(session: Session, owner: str, source_id: UUID) -> tuple[Source, IngestionJob]:
    with session.begin():
        source = session.scalar(
            select(Source).where(Source.owner_id == owner, Source.id == source_id).with_for_update()
        )
        if source is None:
            raise ServiceError("not_found", "Resource not found", 404)
        job = session.scalar(
            select(IngestionJob).where(
                IngestionJob.owner_id == owner,
                IngestionJob.source_id == source_id,
                IngestionJob.status.not_in(TERMINAL),
            )
        )
        # Repeated refresh requests reuse outstanding work rather than queue duplicates.
        if job is not None:
            return source, job
        source.status = Stage.PENDING
        job = IngestionJob(owner_id=owner, source_id=source_id)
        session.add(job)
        session.flush()
    return source, job


def apply_transition(source: Source, job: IngestionJob, change: JobTransition) -> None:
    """Validate before mutating. Persistence/locking is handled by transition_job."""
    counts = {
        field: getattr(change, field) if getattr(change, field) is not None else getattr(job, field)
        for field in ("documents_found", "documents_processed", "chunks_created")
    }
    for field, value in counts.items():
        if value < getattr(job, field):
            raise ServiceError("invalid_progress", "Job counters cannot decrease")
    if counts["documents_processed"] > counts["documents_found"]:
        raise ServiceError(
            "invalid_progress", "Processed documents cannot exceed discovered documents"
        )

    replay = (
        change.status == job.status
        and all(getattr(job, field) == value for field, value in counts.items())
        and change.error_code == job.error_code
        and change.error_detail == job.error_detail
    )
    if replay:
        return
    if job.status in TERMINAL:
        raise ServiceError(
            "terminal_job", "Finished jobs are immutable; refresh the source to retry"
        )
    next_stage = {
        Stage.PENDING: Stage.FETCHING if source.kind == SourceKind.URL else Stage.PARSING,
        Stage.FETCHING: Stage.PARSING,
        Stage.PARSING: Stage.CHUNKING,
        Stage.CHUNKING: Stage.INDEXING,
        Stage.INDEXING: Stage.READY,
    }[Stage(job.status)]
    if change.status not in {job.status, next_stage, Stage.FAILED}:
        raise ServiceError(
            "invalid_transition", f"Cannot transition {job.status} to {change.status}"
        )
    if change.status == Stage.READY and counts["documents_processed"] != counts["documents_found"]:
        raise ServiceError(
            "invalid_progress", "All discovered documents must be processed before ready"
        )

    now = datetime.now(UTC)
    for field, value in counts.items():
        setattr(job, field, value)
    job.status = change.status
    source.status = change.status
    if change.status != Stage.FAILED:
        job.stage = change.status
    job.error_code = change.error_code
    job.error_detail = change.error_detail
    if job.started_at is None and change.status != Stage.PENDING:
        job.started_at = now
    if change.status in TERMINAL:
        job.finished_at = now
    if change.status == Stage.READY:
        source.last_ingested_at = now
    job.updated_at = now
    source.updated_at = now


def transition_job(
    session: Session, owner: str, job_id: UUID, change: JobTransition
) -> IngestionJob:
    with session.begin():
        # Lock source first, consistently with refresh, then lock and reread the job.
        source_id = session.scalar(
            select(IngestionJob.source_id).where(
                IngestionJob.id == job_id, IngestionJob.owner_id == owner
            )
        )
        if source_id is None:
            raise ServiceError("not_found", "Resource not found", 404)
        source = session.scalar(
            select(Source).where(Source.id == source_id, Source.owner_id == owner).with_for_update()
        )
        job = session.scalar(
            select(IngestionJob)
            .where(IngestionJob.id == job_id, IngestionJob.owner_id == owner)
            .with_for_update()
        )
        if source is None or job is None:
            raise ServiceError("not_found", "Resource not found", 404)
        apply_transition(source, job, change)
        session.flush()
    return job
