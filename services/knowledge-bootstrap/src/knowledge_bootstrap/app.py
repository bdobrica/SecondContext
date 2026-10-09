import asyncio
import hmac
import logging
import time
from collections.abc import Iterator
from contextlib import asynccontextmanager
from threading import Event, Thread
from typing import Annotated, Literal
from uuid import UUID

from fastapi import Depends, FastAPI, File, Form, Header, Query, Request, UploadFile
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse, Response
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer
from sqlalchemy import select, text
from sqlalchemy.exc import SQLAlchemyError
from sqlalchemy.orm import Session
from starlette.types import ASGIApp, Receive, Scope, Send

from knowledge_bootstrap.binary import MIME_TYPES as BINARY_MIME_TYPES
from knowledge_bootstrap.binary import select_upload_format
from knowledge_bootstrap.config import Settings
from knowledge_bootstrap.database import make_engine, make_sessions
from knowledge_bootstrap.derived import (
    CandidateView,
    ExtractionRequest,
    ExtractionResponse,
    extract_candidates,
)
from knowledge_bootstrap.ingestion import run_worker
from knowledge_bootstrap.logging import configure_logging
from knowledge_bootstrap.management import delete_source, source_summaries
from knowledge_bootstrap.metrics import SearchMetrics, operational_metrics
from knowledge_bootstrap.models import (
    SCHEMA_REVISION,
    Candidate,
    Chunk,
    Document,
    IngestionJob,
    Source,
)
from knowledge_bootstrap.parsers import ParseError, normalize_input
from knowledge_bootstrap.pipeline import reindex_source
from knowledge_bootstrap.schemas import (
    ChunkView,
    DocumentView,
    JobView,
    SearchRequest,
    SearchResponse,
    SourceAccepted,
    SourceCreate,
    SourceSummaryView,
    SourceView,
    UploadFormat,
)
from knowledge_bootstrap.search import search
from knowledge_bootstrap.service import ServiceError, create_source, get_owned, refresh_source
from knowledge_bootstrap.ui import install_ui

bearer = HTTPBearer(auto_error=False)


class BodyLimit:
    """Bound actual received bytes, including requests without Content-Length."""

    def __init__(self, app: ASGIApp, max_bytes: int):
        self.app = app
        self.max_bytes = max_bytes

    async def __call__(self, scope: Scope, receive: Receive, send: Send):
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        body = bytearray()
        while True:
            message = await receive()
            if message["type"] == "http.disconnect":
                return
            body.extend(message.get("body", b""))
            if len(body) > self.max_bytes:
                response = JSONResponse(
                    {
                        "error": {
                            "code": "request_too_large",
                            "detail": "Request body exceeds limit",
                        }
                    },
                    status_code=413,
                )
                await response(scope, receive, send)
                return
            if not message.get("more_body", False):
                break
        delivered = False

        async def replay():
            nonlocal delivered
            if not delivered:
                delivered = True
                return {"type": "http.request", "body": bytes(body), "more_body": False}
            return await receive()

        await self.app(scope, replay, send)


def session_for(request: Request) -> Iterator[Session]:
    with request.app.state.sessions() as session:
        yield session


def owner_for(
    request: Request,
    credentials: Annotated[HTTPAuthorizationCredentials | None, Depends(bearer)],
) -> str:
    if credentials is not None:
        supplied = credentials.credentials.encode()
        for owner, token in request.app.state.settings.auth_tokens.items():
            if hmac.compare_digest(supplied, token.get_secret_value().encode()):
                return owner
    raise ServiceError("unauthorized", "A valid bearer token is required", 401)


Owner = Annotated[str, Depends(owner_for)]
Database = Annotated[Session, Depends(session_for)]
Limit = Annotated[int, Query(ge=1, le=100)]
Offset = Annotated[int, Query(ge=0, le=1_000_000)]


def create_app(settings: Settings | None = None) -> FastAPI:
    settings = settings or Settings()
    logger = configure_logging(settings.log_level)
    engine = make_engine(settings)

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        stop = Event()
        worker = Thread(target=run_worker, args=(app.state.sessions, settings, stop), daemon=True)
        if settings.text_worker_enabled:
            worker.start()
        logger.info("service started")
        try:
            yield
        finally:
            stop.set()
            if settings.text_worker_enabled:
                await asyncio.to_thread(worker.join)
            engine.dispose()
            logger.info("service stopped")

    app = FastAPI(title="Knowledge Bootstrap", version="0.1.0", lifespan=lifespan)
    app.state.settings = settings
    app.state.sessions = make_sessions(engine)
    app.state.engine = engine
    app.state.search_metrics = SearchMetrics(settings.auth_tokens)
    app.add_middleware(BodyLimit, max_bytes=settings.max_request_bytes)
    install_ui(app, settings)

    @app.post("/v1/sources/{source_id}/extract", response_model=ExtractionResponse)
    def extract(source_id: UUID, payload: ExtractionRequest, owner: Owner, session: Database):
        return extract_candidates(session, settings, owner, source_id, payload)

    @app.get("/v1/candidates", response_model=list[CandidateView])
    def list_candidates(
        owner: Owner,
        session: Database,
        source_id: UUID | None = None,
        document_id: UUID | None = None,
        status: Literal["active", "retracted", "all"] = "active",
        limit: Limit = 50,
        offset: Offset = 0,
    ):
        statement = select(Candidate).where(Candidate.owner_id == owner)
        if source_id is not None:
            statement = statement.where(Candidate.source_id == source_id)
        if document_id is not None:
            statement = statement.where(Candidate.document_id == document_id)
        if status != "all":
            statement = statement.where(Candidate.status == status)
        return session.scalars(
            statement.order_by(Candidate.updated_at, Candidate.id).offset(offset).limit(limit)
        ).all()

    @app.get("/v1/candidates/{candidate_id}", response_model=CandidateView)
    def show_candidate(candidate_id: UUID, owner: Owner, session: Database):
        return get_owned(session, Candidate, owner, candidate_id)

    @app.delete("/v1/candidates/{candidate_id}", status_code=204)
    def purge_candidate(candidate_id: UUID, owner: Owner, session: Database):
        with session.begin():
            candidate = session.scalar(
                select(Candidate)
                .where(
                    Candidate.id == candidate_id,
                    Candidate.owner_id == owner,
                )
                .with_for_update()
            )
            if candidate is not None:
                session.delete(candidate)
        return Response(status_code=204)

    @app.delete("/v1/sources/{source_id}", status_code=204)
    def remove_source(source_id: UUID, owner: Owner, session: Database):
        delete_source(session, settings, owner, source_id)
        return Response(status_code=204)

    @app.post("/v1/search", response_model=SearchResponse, response_model_exclude_none=True)
    def search_evidence(payload: SearchRequest, owner: Owner, session: Database):
        with app.state.search_metrics.measure(owner, payload.mode):
            return search(session, settings, owner, payload)

    @app.get("/v1/metrics")
    def metrics(owner: Owner, session: Database):
        return operational_metrics(session, owner, app.state.search_metrics)

    @app.post("/v1/sources/{source_id}/reindex", response_model=SourceAccepted, status_code=202)
    def reindex(source_id: UUID, owner: Owner, session: Database):
        source, job = reindex_source(session, owner, source_id)
        return SourceAccepted(
            source=SourceView.model_validate(source), job=JobView.model_validate(job)
        )

    @app.middleware("http")
    async def log_request(request: Request, call_next):
        start = time.monotonic()
        response = await call_next(request)
        logger.info(
            "request completed",
            extra={
                "method": request.method,
                "path": request.url.path,
                "status": response.status_code,
                "duration_ms": round((time.monotonic() - start) * 1000, 2),
            },
        )
        return response

    @app.exception_handler(ServiceError)
    async def service_error(request: Request, exc: ServiceError):
        headers = {"WWW-Authenticate": "Bearer"} if exc.status_code == 401 else None
        return JSONResponse(
            {"error": {"code": exc.code, "detail": exc.detail}},
            status_code=exc.status_code,
            headers=headers,
        )

    @app.exception_handler(ParseError)
    async def input_error(request: Request, exc: ParseError):
        return JSONResponse(
            {"error": {"code": exc.code, "detail": exc.detail}},
            status_code=413 if exc.code == "input_too_large" else 422,
        )

    @app.exception_handler(RequestValidationError)
    async def validation_error(request: Request, exc: RequestValidationError):
        # FastAPI's default includes the offending input, which can contain raw source content.
        return JSONResponse(
            {
                "error": {
                    "code": "invalid_request",
                    "detail": "Request validation failed",
                    "fields": [
                        {"location": list(error["loc"]), "message": error["msg"]}
                        for error in exc.errors()
                    ],
                }
            },
            status_code=422,
        )

    @app.exception_handler(SQLAlchemyError)
    async def database_error(request: Request, exc: SQLAlchemyError):
        logger.error("database operation failed", extra={"error_type": type(exc).__name__})
        return JSONResponse(
            {"error": {"code": "database_unavailable", "detail": "Database operation failed"}},
            status_code=503,
        )

    @app.get("/healthz")
    def health():
        return {"status": "ok", "service": "knowledge-bootstrap"}

    @app.get("/readyz")
    def ready(session: Database):
        try:
            version = session.scalar(text("SELECT version_num FROM alembic_version"))
            if version != SCHEMA_REVISION:
                raise ServiceError(
                    "schema_not_ready", "Apply the knowledge service migrations", 503
                )
            # Check connectivity and the canonical tables as well as the revision marker.
            session.execute(
                text(
                    "SELECT 1 FROM knowledge_sources, knowledge_documents, "
                    "knowledge_chunks, knowledge_ingestion_jobs, knowledge_candidates LIMIT 0"
                )
            )
        except SQLAlchemyError:
            return JSONResponse(
                {"status": "not_ready", "detail": "Postgres unavailable or migrations not applied"},
                status_code=503,
            )
        return {"status": "ready", "postgres": "ok"}

    @app.post("/v1/sources", response_model=SourceAccepted, status_code=202)
    def add_source(
        payload: SourceCreate,
        owner: Owner,
        session: Database,
        idempotency_key: Annotated[str | None, Header(min_length=1, max_length=128)] = None,
    ):
        if idempotency_key is not None and not idempotency_key.strip():
            raise ServiceError("invalid_request", "Idempotency-Key must not be blank", 422)
        if payload.kind == "url" and (
            payload.config_json["max_pages"] > settings.web_max_pages
            or payload.config_json["max_depth"] > settings.web_max_depth
        ):
            raise ServiceError(
                "crawl_limit_exceeded", "Source crawl options exceed server limits", 422
            )
        if payload.text is not None:
            payload.text = normalize_input(payload.text, settings)
        source, job = create_source(session, owner, payload, idempotency_key)
        return {"source": source, "job": job}

    @app.post("/v1/sources/upload", response_model=SourceAccepted, status_code=202)
    def upload_file(
        owner: Owner,
        session: Database,
        file: Annotated[UploadFile, File()],
        name: Annotated[str, Form(min_length=1, max_length=500)] = "Untitled",
        format: Annotated[UploadFormat, Form()] = "auto",
        idempotency_key: Annotated[str | None, Header(min_length=1, max_length=128)] = None,
    ):
        if not name.strip() or (idempotency_key is not None and not idempotency_key.strip()):
            raise ServiceError("invalid_request", "Name and Idempotency-Key must not be blank", 422)
        filename = file.filename or "upload.txt"
        if len(filename) > 8192 or "\x00" in filename:
            raise ServiceError("invalid_request", "Invalid original filename", 422)
        data = file.file.read(max(settings.max_input_bytes, settings.max_file_bytes) + 1)
        selected = select_upload_format(data, filename, file.content_type, format)
        binary = selected in BINARY_MIME_TYPES
        if binary and len(data) > settings.max_file_bytes:
            raise ParseError("input_too_large", "File exceeds the upload byte limit")
        text = None if binary else normalize_input(data, settings)
        payload = SourceCreate(
            kind="file",
            name=name,
            source_uri=filename,
            content_type=(file.content_type or "application/octet-stream")[:200],
            format=selected,
            text=text,
        )
        source, job = create_source(
            session, owner, payload, idempotency_key, input_bytes=data if binary else None
        )
        return {"source": source, "job": job}

    @app.get("/v1/sources", response_model=list[SourceSummaryView])
    def list_sources(owner: Owner, session: Database, limit: Limit = 50, offset: Offset = 0):
        return source_summaries(session, owner, limit=limit, offset=offset)

    @app.get("/v1/sources/{source_id}", response_model=SourceSummaryView)
    def show_source(source_id: UUID, owner: Owner, session: Database):
        rows = source_summaries(session, owner, source_id=source_id, limit=1)
        if not rows:
            raise ServiceError("not_found", "Resource not found", 404)
        return rows[0]

    @app.post("/v1/sources/{source_id}/refresh", response_model=SourceAccepted, status_code=202)
    def refresh(source_id: UUID, owner: Owner, session: Database):
        source, job = refresh_source(session, owner, source_id)
        return {"source": source, "job": job}

    @app.get("/v1/sources/{source_id}/jobs", response_model=list[JobView])
    def list_jobs(
        source_id: UUID, owner: Owner, session: Database, limit: Limit = 50, offset: Offset = 0
    ):
        get_owned(session, Source, owner, source_id)
        return session.scalars(
            select(IngestionJob)
            .where(IngestionJob.owner_id == owner, IngestionJob.source_id == source_id)
            .order_by(IngestionJob.created_at.desc(), IngestionJob.id)
            .offset(offset)
            .limit(limit)
        ).all()

    @app.get("/v1/jobs/{job_id}", response_model=JobView)
    def show_job(job_id: UUID, owner: Owner, session: Database):
        return get_owned(session, IngestionJob, owner, job_id)

    @app.get("/v1/sources/{source_id}/documents", response_model=list[DocumentView])
    def list_documents(
        source_id: UUID, owner: Owner, session: Database, limit: Limit = 50, offset: Offset = 0
    ):
        get_owned(session, Source, owner, source_id)
        return session.scalars(
            select(Document)
            .where(Document.owner_id == owner, Document.source_id == source_id)
            .order_by(Document.created_at, Document.id)
            .offset(offset)
            .limit(limit)
        ).all()

    @app.get("/v1/documents/{document_id}", response_model=DocumentView)
    def show_document(document_id: UUID, owner: Owner, session: Database):
        return get_owned(session, Document, owner, document_id)

    @app.get("/v1/documents/{document_id}/chunks", response_model=list[ChunkView])
    def list_chunks(
        document_id: UUID, owner: Owner, session: Database, limit: Limit = 50, offset: Offset = 0
    ):
        get_owned(session, Document, owner, document_id)
        return session.scalars(
            select(Chunk)
            .where(Chunk.owner_id == owner, Chunk.document_id == document_id)
            .order_by(Chunk.ordinal)
            .offset(offset)
            .limit(limit)
        ).all()

    return app


# Factory mode keeps configuration validation explicit and makes tests independent of env files.
logging.getLogger("knowledge_bootstrap").addHandler(logging.NullHandler())
