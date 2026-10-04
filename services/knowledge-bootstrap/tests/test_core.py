from datetime import UTC, datetime
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from pydantic import ValidationError

from knowledge_bootstrap.app import create_app
from knowledge_bootstrap.config import Settings
from knowledge_bootstrap.models import IngestionJob, Source, SourceKind, Stage
from knowledge_bootstrap.schemas import JobTransition, SourceCreate
from knowledge_bootstrap.service import ServiceError, apply_transition


def pair(kind=SourceKind.TEXT):
    source = Source(id=uuid4(), owner_id="a", kind=kind, name="Fixture", status=Stage.PENDING)
    job = IngestionJob(
        id=uuid4(),
        source_id=source.id,
        owner_id="a",
        status=Stage.PENDING,
        stage=Stage.PENDING,
        documents_found=0,
        documents_processed=0,
        chunks_created=0,
        updated_at=datetime.now(UTC),
    )
    return source, job


@pytest.mark.parametrize("kind", list(SourceKind))
def test_pipeline_and_terminal_replay(kind):
    source, job = pair(kind)
    stages = [Stage.PARSING, Stage.CHUNKING, Stage.INDEXING, Stage.READY]
    if kind == SourceKind.URL:
        stages.insert(0, Stage.FETCHING)
    for status in stages:
        apply_transition(
            source,
            job,
            JobTransition(
                status=status, documents_found=2, documents_processed=2, chunks_created=4
            ),
        )
        assert job.status == source.status == job.stage == status
    assert job.started_at is not None
    assert job.finished_at == source.last_ingested_at
    finished = job.finished_at
    updated = job.updated_at
    apply_transition(source, job, JobTransition(status=Stage.READY))
    assert job.finished_at == finished
    assert job.updated_at == updated
    with pytest.raises(ServiceError, match="immutable"):
        apply_transition(source, job, JobTransition(status=Stage.PARSING))


def test_illegal_stage_and_counts_do_not_mutate_job():
    source, job = pair()
    with pytest.raises(ServiceError, match="Cannot transition"):
        apply_transition(source, job, JobTransition(status=Stage.READY))
    assert source.status == job.status == Stage.PENDING
    with pytest.raises(ServiceError, match="cannot exceed"):
        apply_transition(source, job, JobTransition(status=Stage.PARSING, documents_processed=1))
    assert source.status == job.status == Stage.PENDING
    apply_transition(source, job, JobTransition(status=Stage.PARSING, documents_found=2))
    with pytest.raises(ServiceError, match="cannot decrease"):
        apply_transition(source, job, JobTransition(status=Stage.PARSING, documents_found=1))
    with pytest.raises(ServiceError, match="Cannot transition"):
        apply_transition(source, job, JobTransition(status=Stage.FETCHING))


def test_ready_requires_finished_documents():
    source, job = pair()
    for status in [Stage.PARSING, Stage.CHUNKING, Stage.INDEXING]:
        apply_transition(source, job, JobTransition(status=status, documents_found=2))
    with pytest.raises(ServiceError, match="All discovered documents"):
        apply_transition(source, job, JobTransition(status=Stage.READY, documents_processed=1))
    assert job.status == Stage.INDEXING


@pytest.mark.parametrize("stage", [Stage.PENDING, Stage.PARSING, Stage.CHUNKING, Stage.INDEXING])
def test_failure_preserves_failed_stage(stage):
    source, job = pair()
    job.status = job.stage = stage
    change = JobTransition(
        status=Stage.FAILED, error_code="parser_failed", error_detail="Bad input"
    )
    apply_transition(source, job, change)
    assert source.status == job.status == Stage.FAILED
    assert job.stage == stage
    assert job.error_code == "parser_failed"
    assert job.started_at and job.finished_at
    finished = job.finished_at
    apply_transition(source, job, change)
    assert job.finished_at == finished
    with pytest.raises(ServiceError, match="immutable"):
        apply_transition(source, job, JobTransition(status=Stage.PARSING))


@pytest.mark.parametrize(
    "change",
    [
        {"status": "failed"},
        {"status": "failed", "error_code": "bad", "error_detail": " "},
        {"status": "failed", "error_code": "BAD CODE", "error_detail": "bad"},
        {"status": "parsing", "error_code": "bad"},
        {"status": "parsing", "documents_found": -1},
        {"status": "parsing", "documents_found": 2_147_483_648},
    ],
)
def test_invalid_transition_inputs(change):
    with pytest.raises(ValidationError):
        JobTransition(**change)


@pytest.mark.parametrize(
    "overrides",
    [
        {"database_url": "sqlite:///knowledge.db"},
        {"database_url": "postgresql+psycopg://localhost"},
        {"auth_tokens": {}},
        {"auth_tokens": {"a": "same-token-123456", "b": "same-token-123456"}},
        {"auth_tokens": {"": "long-enough-test-token"}},
        {"auth_tokens": {"a": "short"}},
        {"auth_tokens": {"a": "token has whitespace"}},
        {"qdrant_url": "file:///tmp/socket"},
        {"database_pool_size": 0},
        {"log_level": "nonsense"},
    ],
)
def test_invalid_configuration(settings, overrides):
    with pytest.raises(ValidationError):
        Settings(**(settings.model_dump() | overrides))


def test_env_configuration_and_secret_redaction(monkeypatch):
    monkeypatch.setenv(
        "KNOWLEDGE_DATABASE_URL", "postgresql+psycopg://user:private-password@localhost/knowledge"
    )
    monkeypatch.setenv("KNOWLEDGE_AUTH_TOKENS", '{"team":"private-test-token-123456"}')
    monkeypatch.setenv("KNOWLEDGE_QDRANT_API_KEY", "private-qdrant-key")
    settings = Settings()
    assert settings.auth_tokens["team"].get_secret_value() == "private-test-token-123456"
    for secret in ["private-password", "private-test-token-123456", "private-qdrant-key"]:
        assert secret not in repr(settings)


@pytest.mark.parametrize(
    "payload",
    [
        {"kind": "url", "name": "x", "source_uri": "file:///etc/passwd"},
        {"kind": "url", "name": "x", "source_uri": "https://user:pass@example.com"},
        {"kind": "file", "name": "x"},
        {"kind": "text", "name": " "},
        {"kind": "text", "name": "x", "text": "\u0000bad"},
        {"kind": "text", "name": "x", "owner_id": "other"},
    ],
)
def test_invalid_source_input(payload):
    with pytest.raises(ValidationError):
        SourceCreate(**payload)


def test_liveness_auth_and_bounded_body_without_postgres(settings):
    with TestClient(create_app(settings)) as client:
        assert client.get("/healthz").json()["status"] == "ok"
        assert client.get("/v1/sources").status_code == 401
        assert (
            client.get("/v1/sources", headers={"Authorization": "Bearer unknown"}).status_code
            == 401
        )
        assert (
            client.post("/v1/sources", content=b"x" * (settings.max_request_bytes + 1)).status_code
            == 413
        )
        # A streamed body without a Content-Length receives the same limit.
        part_size = settings.max_request_bytes // 2 + 1
        streamed = client.post("/v1/sources", content=iter([b"x" * part_size, b"x" * part_size]))
        assert streamed.status_code == 413
