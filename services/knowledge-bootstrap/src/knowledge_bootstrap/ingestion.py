"""Durable K2 worker: canonical parsing only; K5 will consume chunking jobs."""

import hashlib
import logging
from threading import Event

from sqlalchemy import select
from sqlalchemy.exc import SQLAlchemyError
from sqlalchemy.orm import sessionmaker

from knowledge_bootstrap.config import Settings
from knowledge_bootstrap.models import Document, IngestionJob, Source, SourceKind, Stage
from knowledge_bootstrap.parsers import ParseError, detect_format, normalize_input, parse_text
from knowledge_bootstrap.schemas import JobTransition
from knowledge_bootstrap.service import apply_transition

logger = logging.getLogger("knowledge_bootstrap")
PARSE_STATES = (Stage.PENDING, Stage.PARSING)
MIME_TYPES = {
    "text": "text/plain",
    "markdown": "text/markdown",
    "json": "application/json",
    "yaml": "application/yaml",
}


def process_next(sessions: sessionmaker, settings: Settings) -> bool:
    """Claim and parse one input atomically. A crash rolls back to a claimable job.

    Source locks precede job locks, matching refresh/transition. SKIP LOCKED allows
    multiple API processes to poll without duplicate parsing or a separate queue.
    Only bounded local work runs inside this transaction; no network calls.
    """
    with sessions.begin() as session:
        source = session.scalar(
            select(Source)
            .join(
                IngestionJob,
                (IngestionJob.source_id == Source.id) & (IngestionJob.owner_id == Source.owner_id),
            )
            .where(
                Source.kind.in_((SourceKind.TEXT, SourceKind.FILE)),
                Source.input_text.is_not(None),
                IngestionJob.status.in_(PARSE_STATES),
                IngestionJob.documents_found <= 1,
                IngestionJob.documents_processed <= 1,
                IngestionJob.chunks_created == 0,
                Source.owner_id.in_(settings.auth_tokens),
            )
            .order_by(IngestionJob.created_at, IngestionJob.id)
            .with_for_update(of=Source, skip_locked=True)
            .limit(1)
        )
        if source is None:
            return False
        job = session.scalar(
            select(IngestionJob)
            .where(
                IngestionJob.source_id == source.id,
                IngestionJob.owner_id == source.owner_id,
                IngestionJob.status.in_(PARSE_STATES),
            )
            .with_for_update()
        )
        # A waiting source lock can observe a job completed by an operator.
        if job is None:
            return False
        apply_transition(source, job, JobTransition(status=Stage.PARSING, documents_found=1))
        try:
            text = normalize_input(source.input_text, settings)
            source.format = source.format or detect_format(text)
            parsed = parse_text(text, source.format, settings)
        except ParseError as exc:
            apply_transition(
                source,
                job,
                JobTransition(status=Stage.FAILED, error_code=exc.code, error_detail=exc.detail),
            )
            logger.info(
                "text parsing failed", extra={"job_id": str(job.id), "error_code": exc.code}
            )
        else:
            # The source UUID is the stable document URI; filenames are metadata, never paths.
            uri = f"urn:knowledge:source:{source.id}"
            document = session.scalar(
                select(Document).where(
                    Document.owner_id == source.owner_id,
                    Document.source_id == source.id,
                    Document.uri == uri,
                )
            )
            if document is None:
                document = Document(owner_id=source.owner_id, source_id=source.id, uri=uri)
                session.add(document)
            document.title = (
                source.name if source.name != "Untitled" else parsed.title or source.name
            )
            document.format = parsed.format
            document.mime_type = MIME_TYPES[parsed.format]
            document.raw_content_or_ref = uri  # Original decoded text remains on the source.
            document.text_content = parsed.text
            document.content_hash = parsed.content_hash
            document.metadata_json = {
                **parsed.metadata(),
                "source_uri": source.source_uri,
                "declared_content_type": source.content_type,
            }
            source.format = parsed.format
            source.content_hash = hashlib.sha256(source.input_text.encode()).hexdigest()
            apply_transition(
                source,
                job,
                JobTransition(status=Stage.CHUNKING, documents_found=1, documents_processed=1),
            )
            logger.info("text parsed", extra={"job_id": str(job.id), "format": parsed.format})
        session.flush()
    return True


def run_worker(sessions: sessionmaker, settings: Settings, stop: Event) -> None:
    while not stop.is_set():
        try:
            if process_next(sessions, settings):
                continue
        except SQLAlchemyError as exc:
            # The transaction rolled back. Retry durably on the next poll, without logging input.
            logger.error("text worker database failure", extra={"error_type": type(exc).__name__})
        except Exception as exc:
            logger.error("text worker failure", extra={"error_type": type(exc).__name__})
        stop.wait(settings.worker_poll_seconds)
