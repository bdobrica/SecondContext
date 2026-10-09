"""Durable parser stage plus canonical chunking and search projection polling."""

import hashlib
import json
import logging
from threading import Event

from sqlalchemy import delete, func, or_, select
from sqlalchemy.exc import SQLAlchemyError
from sqlalchemy.orm import sessionmaker

from knowledge_bootstrap.binary import MIME_TYPES as BINARY_MIME_TYPES
from knowledge_bootstrap.binary import parse_binary
from knowledge_bootstrap.config import Settings
from knowledge_bootstrap.models import Document, IngestionJob, Source, SourceKind, Stage
from knowledge_bootstrap.parsers import ParseError, detect_format, normalize_input, parse_text
from knowledge_bootstrap.pipeline import process_chunk_next, process_index_next
from knowledge_bootstrap.schemas import JobTransition
from knowledge_bootstrap.service import apply_transition
from knowledge_bootstrap.web import ingest_website

logger = logging.getLogger("knowledge_bootstrap")
PARSE_STATES = (Stage.PENDING, Stage.PARSING)
WEB_STATES = (Stage.PENDING, Stage.FETCHING)
MIME_TYPES = {
    "text": "text/plain",
    "markdown": "text/markdown",
    "json": "application/json",
    "yaml": "application/yaml",
    **BINARY_MIME_TYPES,
}


def process_next(sessions: sessionmaker, settings: Settings) -> bool:
    """Claim and parse one input atomically. A crash rolls back to a claimable job.

    Source locks precede job locks, matching refresh/transition. SKIP LOCKED allows
    multiple API processes to poll without duplicate parsing or a separate queue.
    Local parsing and website crawling run in bounded disposable processes. URL
    work holds the source lock for at most the configured crawl time plus startup.
    Network/DB crashes leave the original pending job claimable on restart.
    """
    with sessions.begin() as session:
        source = session.scalar(
            select(Source)
            .join(
                IngestionJob,
                (IngestionJob.source_id == Source.id) & (IngestionJob.owner_id == Source.owner_id),
            )
            .where(
                or_(
                    (Source.kind.in_((SourceKind.TEXT, SourceKind.FILE)))
                    & (or_(Source.input_text.is_not(None), Source.input_bytes.is_not(None)))
                    & (IngestionJob.status.in_(PARSE_STATES))
                    & (IngestionJob.documents_found <= 1)
                    & (IngestionJob.documents_processed <= 1),
                    (Source.kind == SourceKind.URL)
                    & (IngestionJob.status.in_(WEB_STATES))
                    & (IngestionJob.documents_found == 0)
                    & (IngestionJob.documents_processed == 0),
                ),
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
                IngestionJob.status.in_(
                    WEB_STATES if source.kind == SourceKind.URL else PARSE_STATES
                ),
            )
            .with_for_update()
        )
        # A waiting source lock can observe a job completed by an operator.
        if job is None:
            return False
        if source.kind == SourceKind.URL:
            process_website(session, source, job, settings)
            session.flush()
            return True
        apply_transition(source, job, JobTransition(status=Stage.PARSING, documents_found=1))
        if source.input_bytes is None:
            source.format = source.format or detect_format(source.input_text)
        uri = f"urn:knowledge:source:{source.id}"
        document = session.scalar(
            select(Document).where(Document.source_id == source.id, Document.uri == uri)
        )
        input_hash = hashlib.sha256(
            source.input_bytes if source.input_bytes is not None else source.input_text.encode()
        ).hexdigest()
        fingerprint = hashlib.sha256(
            json.dumps(
                {
                    "version": 1,
                    "input_hash": input_hash,
                    "format": source.format,
                    "name": source.name,
                    "config": source.config_json,
                    "limits": {
                        key: value
                        for key, value in settings.model_dump().items()
                        if key.startswith(("max_", "min_pdf_", "parser_"))
                    },
                },
                sort_keys=True,
            ).encode()
        ).hexdigest()
        if document is not None and document.metadata_json.get("input_fingerprint") == fingerprint:
            source.content_hash = input_hash
            apply_transition(
                source,
                job,
                JobTransition(status=Stage.CHUNKING, documents_found=1, documents_processed=1),
            )
            return True
        try:
            if source.input_bytes is not None:
                parsed = parse_binary(source.input_bytes, source.format, settings)
            else:
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
                "document parsing failed", extra={"job_id": str(job.id), "error_code": exc.code}
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
            document.raw_content_or_ref = uri  # Original text/bytes remain on the source.
            document.text_content = parsed.text
            document.content_hash = parsed.content_hash
            parsed.title, parsed.uri = document.title, uri
            document.metadata_json = {
                **(document.metadata_json or {}),
                **parsed.metadata(),
                "input_fingerprint": fingerprint,
                "source_uri": source.source_uri,
                "declared_content_type": source.content_type,
            }
            source.format = parsed.format
            source.content_hash = input_hash
            source.metadata_json = {**source.metadata_json, "chunks_current": False}
            apply_transition(
                source,
                job,
                JobTransition(status=Stage.CHUNKING, documents_found=1, documents_processed=1),
            )
            logger.info("document parsed", extra={"job_id": str(job.id), "format": parsed.format})
        session.flush()
    return True


def process_website(session, source: Source, job: IngestionJob, settings: Settings) -> None:
    apply_transition(source, job, JobTransition(status=Stage.FETCHING))
    try:
        result = ingest_website(source.source_uri, source.config_json, settings)
        observed = {doc.extra_metadata["final_url"] for doc in result.documents}
        complete = (
            result.metadata.get("frontier_complete") is True
            and not result.metadata.get("skipped_pages")
            and not result.metadata.get("frontier_truncated")
            and not result.metadata.get("page_limit_reached")
        )
        missing = set(result.metadata.get("missing_pages", [])) - observed
        retained = (
            0
            if complete
            else session.scalar(
                select(func.count(Document.id)).where(
                    Document.source_id == source.id,
                    Document.owner_id == source.owner_id,
                    Document.uri.not_in(observed | missing),
                )
            )
        )
        if retained + len(observed) > settings.web_max_pages:
            raise ParseError(
                "crawl_limit_exceeded",
                "Refresh exceeds the retained website page limit; retry with a complete crawl",
            )
    except ParseError as exc:
        apply_transition(
            source,
            job,
            JobTransition(status=Stage.FAILED, error_code=exc.code, error_detail=exc.detail),
        )
        logger.info(
            "website ingestion failed", extra={"job_id": str(job.id), "error_code": exc.code}
        )
        return
    apply_transition(
        source, job, JobTransition(status=Stage.PARSING, documents_found=len(result.documents))
    )
    for parsed in result.documents:
        # The observed final URL is authoritative; an HTML canonical hint is metadata.
        uri = parsed.extra_metadata["final_url"]
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
        document.title = parsed.title or source.name
        document.format = "html"
        document.mime_type = parsed.extra_metadata["content_type"].split(";", 1)[0].strip().lower()
        document.raw_content_or_ref = uri  # Refetchable; raw HTML is deliberately not retained.
        document.text_content = parsed.text
        document.content_hash = parsed.content_hash
        parsed.title, parsed.uri = document.title, uri
        document.metadata_json = {
            **(document.metadata_json or {}),
            **parsed.metadata(),
            "source_uri": source.source_uri,
        }
    # Only an exhausted, successful frontier is authoritative for absence. A capped
    # or partially failed crawl updates observed pages and retains all unvisited ones.
    if complete:
        session.execute(
            delete(Document).where(
                Document.source_id == source.id,
                Document.owner_id == source.owner_id,
                Document.uri.not_in(observed),
            )
        )
    else:
        # Explicit 404/410 responses can remove previously observed URLs even when
        # unrelated targets failed. Never remove a URL that this crawl did observe.
        if missing:
            session.execute(
                delete(Document).where(
                    Document.source_id == source.id,
                    Document.owner_id == source.owner_id,
                    Document.uri.in_(missing),
                )
            )
    session.flush()
    canonical = session.scalars(
        select(Document).where(
            Document.source_id == source.id, Document.owner_id == source.owner_id
        )
    ).all()
    source.format = "html"
    source.content_type = result.documents[0].extra_metadata["content_type"]
    source.content_hash = hashlib.sha256(
        "\n".join(sorted(doc.uri + " " + doc.content_hash for doc in canonical)).encode()
    ).hexdigest()
    source.metadata_json = {
        **source.metadata_json,
        "chunks_current": False,
        "crawl": {
            **result.metadata,
            "absence_cleanup": complete,
            "canonical_documents": len(canonical),
        },
    }
    apply_transition(
        source,
        job,
        JobTransition(
            status=Stage.CHUNKING,
            documents_found=len(result.documents),
            documents_processed=len(result.documents),
        ),
    )
    logger.info(
        "website parsed",
        extra={"job_id": str(job.id), "documents_processed": len(result.documents)},
    )


def run_worker(sessions: sessionmaker, settings: Settings, stop: Event) -> None:
    while not stop.is_set():
        try:
            if (
                process_next(sessions, settings)
                or process_chunk_next(sessions, settings)
                or process_index_next(sessions, settings)
            ):
                continue
        except SQLAlchemyError as exc:
            # The transaction rolled back. Retry durably on the next poll, without logging input.
            logger.error(
                "ingestion worker database failure", extra={"error_type": type(exc).__name__}
            )
        except Exception as exc:
            logger.error("ingestion worker failure", extra={"error_type": type(exc).__name__})
        stop.wait(settings.worker_poll_seconds)
