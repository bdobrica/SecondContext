from concurrent.futures import ThreadPoolExecutor
from dataclasses import replace
from uuid import UUID

import pytest
from sqlalchemy import func, select
from sqlalchemy.orm import Session

from knowledge_bootstrap import ingestion
from knowledge_bootstrap.ingestion import process_next
from knowledge_bootstrap.models import Document, IngestionJob, Source, Stage
from knowledge_bootstrap.parsers import ParseError
from knowledge_bootstrap.schemas import JobTransition, SourceCreate
from knowledge_bootstrap.service import create_source, transition_job
from knowledge_bootstrap.web_crawl import CrawlResult
from knowledge_bootstrap.web_html import parse_html
from tests.test_web import response

pytestmark = pytest.mark.integration


@pytest.fixture
def website(monkeypatch, db_settings):
    first, _ = parse_html(response(), db_settings)
    second, _ = parse_html(
        replace(
            response(),
            requested_url="https://example.com/docs/setup",
            final_url="https://example.com/docs/setup",
        ),
        db_settings,
    )
    result = CrawlResult([first, second], {"scope": "path", "documents": 2})
    calls = []

    def ingest(url, config, settings):
        calls.append((url, config))
        return result

    monkeypatch.setattr(ingestion, "ingest_website", ingest)
    return result, calls


def add_url(client, **fields):
    return client.post(
        "/v1/sources",
        json={
            "kind": "url",
            "source_uri": "https://example.com/docs",
            "config_json": {"scope": "path", "max_pages": 2},
            **fields,
        },
        headers={"Idempotency-Key": "web-once"},
    )


def test_durable_multi_page_provenance_replay_and_ownership(client, sessions, db_settings, website):
    created = add_url(client)
    assert created.status_code == 202
    data = created.json()
    source_id, job_id = data["source"]["id"], data["job"]["id"]
    assert data["source"]["config_json"] == {"scope": "path", "max_pages": 2, "max_depth": 2}
    assert process_next(sessions, db_settings)
    assert not process_next(sessions, db_settings)
    job = client.get(f"/v1/jobs/{job_id}").json()
    assert job["status"] == "chunking" and job["stage"] == "chunking"
    assert job["documents_found"] == job["documents_processed"] == 2
    assert job["chunks_created"] == 0 and job["finished_at"] is None
    documents = client.get(f"/v1/sources/{source_id}/documents").json()
    assert len(documents) == 2
    assert {doc["uri"] for doc in documents} == {
        "https://example.com/docs",
        "https://example.com/docs/setup",
    }
    for doc in documents:
        assert doc["format"] == "html" and doc["mime_type"] == "text/html"
        assert doc["title"] == "Engineering Handbook"
        assert doc["metadata_json"]["source_uri"] == "https://example.com/docs"
        assert doc["metadata_json"]["final_url"] == doc["uri"]
        assert doc["metadata_json"]["http_status"] == 200
        assert doc["metadata_json"]["retrieved_at"]
        assert doc["metadata_json"]["blocks"]
        assert client.get(f"/v1/documents/{doc['id']}/chunks").json() == []
        assert (
            client.get(
                f"/v1/documents/{doc['id']}",
                headers={"Authorization": "Bearer test-owner-b-token-456"},
            ).status_code
            == 404
        )
    source = client.get(f"/v1/sources/{source_id}").json()
    assert source["format"] == "html" and source["content_hash"]
    assert source["metadata_json"]["crawl"]["documents"] == 2
    assert source["last_ingested_at"] is None
    assert add_url(client).json()["job"]["id"] == job_id
    assert add_url(client, name="Changed").status_code == 409
    assert len(website[1]) == 1


def test_refresh_reuses_final_url_document_ids(client, sessions, db_settings, website, owners):
    created = add_url(client).json()
    assert process_next(sessions, db_settings)
    source_id = created["source"]["id"]
    original = {doc["uri"]: doc for doc in client.get(f"/v1/sources/{source_id}/documents").json()}
    with sessions() as session:
        transition_job(
            session,
            owners[0],
            UUID(created["job"]["id"]),
            JobTransition(
                status=Stage.FAILED, error_code="downstream_failed", error_detail="Retry"
            ),
        )
    website[0].documents[0].text += "\nChanged upstream."
    client.post(f"/v1/sources/{source_id}/refresh")
    assert process_next(sessions, db_settings)
    for doc in client.get(f"/v1/sources/{source_id}/documents").json():
        assert doc["id"] == original[doc["uri"]]["id"]
    current = client.get(f"/v1/sources/{source_id}/documents").json()
    assert (
        next(doc for doc in current if doc["uri"] == "https://example.com/docs")["content_hash"]
        != original["https://example.com/docs"]["content_hash"]
    )


def test_durable_web_failure_retry_and_next_text_job(client, sessions, db_settings, monkeypatch):
    def fail(*args):
        raise ParseError("url_forbidden", "Only public Internet addresses may be fetched")

    monkeypatch.setattr(ingestion, "ingest_website", fail)
    created = add_url(client).json()
    assert process_next(sessions, db_settings)
    job_id, source_id = created["job"]["id"], created["source"]["id"]
    failed = client.get(f"/v1/jobs/{job_id}").json()
    assert failed["status"] == "failed" and failed["stage"] == "fetching"
    assert failed["error_code"] == "url_forbidden" and failed["finished_at"]
    assert client.get(f"/v1/sources/{source_id}/documents").json() == []
    retry = client.post(f"/v1/sources/{source_id}/refresh").json()
    assert retry["job"]["id"] != job_id
    assert process_next(sessions, db_settings)
    assert client.get(f"/v1/jobs/{job_id}").json() == failed
    client.post("/v1/sources", json={"kind": "text", "text": "Continue processing"})
    assert process_next(sessions, db_settings)


def test_web_owner_claim_and_concurrency(sessions, owners, db_settings, website):
    with sessions() as session:
        source, job = create_source(
            session,
            owners[0],
            SourceCreate(
                kind="url",
                source_uri="https://example.com/docs",
                config_json={"scope": "path", "max_pages": 2},
            ),
        )
    other = db_settings.model_copy(
        update={"auth_tokens": {owners[1]: db_settings.auth_tokens[owners[1]]}}
    )
    assert not process_next(sessions, other)
    with ThreadPoolExecutor(max_workers=6) as executor:
        assert sum(executor.map(lambda _: process_next(sessions, db_settings), range(6))) == 1
    assert len(website[1]) == 1
    with sessions() as session:
        assert (
            session.scalar(
                select(func.count()).select_from(Document).where(Document.source_id == source.id)
            )
            == 2
        )
        assert session.get(IngestionJob, job.id).status == Stage.CHUNKING


def test_web_crash_rolls_back_entire_snapshot(sessions, owners, db_settings, website, monkeypatch):
    with sessions() as session:
        _, job = create_source(
            session,
            owners[0],
            SourceCreate(
                kind="url",
                source_uri="https://example.com/docs",
                config_json={"scope": "path", "max_pages": 2},
            ),
        )
    flush = Session.flush

    def interrupted(session, *args, **kwargs):
        if any(isinstance(row, Document) for row in session.new):
            raise RuntimeError("simulated persistence failure")
        return flush(session, *args, **kwargs)

    with monkeypatch.context() as patch:
        patch.setattr(Session, "flush", interrupted)
        with pytest.raises(RuntimeError):
            process_next(sessions, db_settings)
    with sessions() as session:
        assert session.get(IngestionJob, job.id).status == Stage.PENDING
        assert session.scalar(select(func.count()).select_from(Document)) == 0
    assert process_next(sessions, db_settings)


@pytest.mark.parametrize(
    "fields",
    [
        {"source_uri": "http://127.0.0.1/"},
        {"source_uri": "http://169.254.169.254/"},
        {"source_uri": "https://user:pass@example.com/"},
        {"source_uri": "https://example.com:8443/"},
        {"config_json": {"scope": "domain"}},
        {"config_json": {"scope": "host", "max_pages": 21}},
        {"config_json": {"scope": "path", "max_depth": 4}},
        {"config_json": {"ignore_robots": True}},
        {"format": "pdf"},
    ],
)
def test_api_web_validation_creates_no_job(client, sessions, fields):
    assert add_url(client, **fields).status_code == 422
    with sessions() as session:
        assert session.scalar(select(func.count()).select_from(Source)) == 0


def test_real_disposable_child_rejects_private_dns(settings):
    # No private HTTP server is ever contacted. localhost.localdomain resolves via
    # DNS on deployments where present; numeric rejection is deterministic here.
    from knowledge_bootstrap.web import ingest_website

    with pytest.raises(ParseError) as error:
        ingest_website("http://127.0.0.1/", {}, settings)
    assert error.value.code == "url_forbidden"


def test_legacy_invalid_url_options_fail_durably(sessions, owners, db_settings):
    with sessions() as session:
        source, job = create_source(
            session, owners[0], SourceCreate(kind="url", source_uri="https://example.com/docs")
        )
    with sessions.begin() as session:
        stored = session.get(Source, source.id)
        stored.config_json = {"legacy_arbitrary_option": True}
    assert process_next(sessions, db_settings)
    with sessions() as session:
        failed = session.get(IngestionJob, job.id)
        assert failed.status == Stage.FAILED and failed.error_code == "invalid_crawl_config"
    assert not process_next(sessions, db_settings)
