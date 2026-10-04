import time
from concurrent.futures import ThreadPoolExecutor
from uuid import UUID

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import func, select
from sqlalchemy.orm import Session

from knowledge_bootstrap.app import create_app
from knowledge_bootstrap.ingestion import process_next
from knowledge_bootstrap.models import Document, IngestionJob, Stage
from knowledge_bootstrap.schemas import JobTransition, SourceCreate
from knowledge_bootstrap.service import create_source, transition_job

pytestmark = pytest.mark.integration


def test_pasted_text_auto_title_provenance_and_isolation(client, sessions, db_settings):
    created = client.post(
        "/v1/sources",
        json={"kind": "text", "format": "auto", "text": "# Handbook\r\n\r\nDeploy safely."},
    ).json()
    source_id, job_id = created["source"]["id"], created["job"]["id"]
    assert created["source"]["name"] == "Untitled"
    assert process_next(sessions, db_settings)
    assert not process_next(sessions, db_settings)
    source = client.get(f"/v1/sources/{source_id}").json()
    assert source["status"] == "chunking" and source["format"] == "markdown"
    assert source["content_hash"] and source["last_ingested_at"] is None
    job = client.get(f"/v1/jobs/{job_id}").json()
    assert job["stage"] == "chunking"
    assert job["documents_found"] == job["documents_processed"] == 1
    assert job["chunks_created"] == 0 and job["finished_at"] is None
    documents = client.get(f"/v1/sources/{source_id}/documents").json()
    assert len(documents) == 1
    document = documents[0]
    assert document["title"] == "Handbook"
    assert "\r" not in document["text_content"]
    assert document["uri"] == f"urn:knowledge:source:{source_id}"
    assert document["metadata_json"]["blocks"][0]["type"] == "heading"
    assert client.get(f"/v1/documents/{document['id']}/chunks").json() == []
    assert (
        client.get(
            f"/v1/documents/{document['id']}",
            headers={"Authorization": "Bearer test-owner-b-token-456"},
        ).status_code
        == 404
    )


@pytest.mark.parametrize(
    "filename,content,format,expected",
    [
        ("../policy.txt", b"two paragraphs\n\nkeep both", "auto", "text"),
        ("policy.md", b"# Policy\nDeploy safely.", "auto", "markdown"),
        ("policy.json", b'{"required":2}', "auto", "json"),
        ("policy.yaml", b"required: 2\nenabled: true", "auto", "yaml"),
        ("policy.json", b"# Literal title", "text", "text"),
        ("unknown.dat", b"required: 2", "yaml", "yaml"),
    ],
)
def test_uploaded_formats_and_untrusted_filename(
    client, sessions, db_settings, filename, content, format, expected
):
    created = client.post(
        "/v1/sources/upload",
        files={"file": (filename, content, "application/octet-stream")},
        data={"format": format, "name": "Upload"},
        headers={"Idempotency-Key": "upload-once"},
    )
    assert created.status_code == 202
    source = created.json()["source"]
    assert source["kind"] == "file" and source["source_uri"] == filename
    assert process_next(sessions, db_settings)
    document = client.get(f"/v1/sources/{source['id']}/documents").json()[0]
    assert document["format"] == expected and document["title"] == "Upload"
    assert document["metadata_json"]["source_uri"] == filename
    assert document["mime_type"] != "application/octet-stream"
    replay = client.post(
        "/v1/sources/upload",
        files={"file": (filename, content, "application/octet-stream")},
        data={"format": format, "name": "Upload"},
        headers={"Idempotency-Key": "upload-once"},
    )
    assert replay.json()["source"]["id"] == source["id"]


@pytest.mark.parametrize(
    "format,text,error",
    [
        ("json", '{"bad":', "invalid_json"),
        ("yaml", "!!python/object/apply:os.system [secret]", "invalid_yaml"),
        ("json", "[" * 100 + "0" + "]" * 100, "nesting_too_deep"),
    ],
)
def test_durable_parser_failure_and_retry(client, sessions, db_settings, format, text, error):
    created = client.post(
        "/v1/sources", json={"kind": "text", "text": text, "format": format}
    ).json()
    assert process_next(sessions, db_settings)
    source_id = created["source"]["id"]
    job_id = created["job"]["id"]
    job = client.get(f"/v1/jobs/{job_id}").json()
    assert job["status"] == "failed" and job["stage"] == "parsing"
    assert job["error_code"] == error and "secret" not in job["error_detail"]
    assert job["finished_at"]
    assert client.get(f"/v1/sources/{source_id}/documents").json() == []
    retry = client.post(f"/v1/sources/{source_id}/refresh").json()
    assert retry["job"]["id"] != job_id
    assert process_next(sessions, db_settings)
    assert client.get(f"/v1/jobs/{retry['job']['id']}").json()["error_code"] == error
    assert client.get(f"/v1/jobs/{job_id}").json() == job


def test_refresh_reuses_document_after_failed_downstream_job(client, sessions, db_settings, owners):
    created = client.post("/v1/sources", json={"kind": "text", "text": "Policy"}).json()
    assert process_next(sessions, db_settings)
    source_id = created["source"]["id"]
    original = client.get(f"/v1/sources/{source_id}/documents").json()[0]
    with sessions() as session:
        transition_job(
            session,
            owners[0],
            UUID(created["job"]["id"]),
            JobTransition(
                status=Stage.FAILED, error_code="downstream_failed", error_detail="Retry"
            ),
        )
    client.post(f"/v1/sources/{source_id}/refresh")
    assert process_next(sessions, db_settings)
    documents = client.get(f"/v1/sources/{source_id}/documents").json()
    assert len(documents) == 1
    assert documents[0]["id"] == original["id"]
    assert documents[0]["content_hash"] == original["content_hash"]


def test_worker_concurrency_claim_and_ownership(sessions, owners, db_settings):
    with sessions() as session:
        source, job = create_source(session, owners[0], SourceCreate(kind="text", text="Policy"))
    # A worker configured only for the other owner cannot claim this source.
    other = db_settings.model_copy(
        update={"auth_tokens": {owners[1]: db_settings.auth_tokens[owners[1]]}}
    )
    assert not process_next(sessions, other)
    with ThreadPoolExecutor(max_workers=8) as executor:
        assert sum(executor.map(lambda _: process_next(sessions, db_settings), range(8))) == 1
    with sessions() as session:
        assert (
            session.scalar(
                select(func.count()).select_from(Document).where(Document.source_id == source.id)
            )
            == 1
        )
        assert session.get(IngestionJob, job.id).status == Stage.CHUNKING


def test_worker_transaction_rollback_and_restart_recovery(
    sessions, owners, db_settings, monkeypatch
):
    with sessions() as session:
        _, job = create_source(session, owners[0], SourceCreate(kind="text", text="Policy"))
    original = Session.flush

    def fail_document_flush(session, *args, **kwargs):
        if any(isinstance(row, Document) for row in session.new):
            raise RuntimeError("simulated interrupted persistence")
        return original(session, *args, **kwargs)

    with monkeypatch.context() as patch:
        patch.setattr(Session, "flush", fail_document_flush)
        with pytest.raises(RuntimeError, match="interrupted persistence"):
            process_next(sessions, db_settings)
    with sessions() as session:
        assert session.get(IngestionJob, job.id).status == Stage.PENDING
        assert session.scalar(select(func.count()).select_from(Document)) == 0
    assert process_next(sessions, db_settings)
    with sessions() as session:
        assert session.get(IngestionJob, job.id).status == Stage.CHUNKING


def test_actual_lifespan_worker_processes_durable_pending_jobs(sessions, owners, db_settings):
    with sessions() as session:
        _, job = create_source(session, owners[0], SourceCreate(kind="text", text="Policy"))
    enabled = db_settings.model_copy(
        update={"text_worker_enabled": True, "worker_poll_seconds": 0.05}
    )
    with TestClient(create_app(enabled)) as api:
        api.headers["Authorization"] = "Bearer test-owner-a-token-123"
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            state = api.get(f"/v1/jobs/{job.id}").json()["status"]
            if state == "chunking":
                break
            time.sleep(0.05)
        assert state == "chunking"


def test_text_and_upload_limits_and_validation(client, sessions, db_settings):
    assert (
        client.post(
            "/v1/sources",
            json={"kind": "text", "text": "é" * (db_settings.max_input_bytes // 2 + 1)},
        ).status_code
        == 413
    )
    assert (
        client.post("/v1/sources/upload", files={"file": ("binary.txt", b"\xff\x00bad")}).json()[
            "error"
        ]["code"]
        == "invalid_encoding"
    )
    assert (
        client.post(
            "/v1/sources/upload",
            files={"file": ("large.txt", b"x" * (db_settings.max_input_bytes + 1))},
        ).status_code
        == 413
    )
    assert (
        client.post(
            "/v1/sources/upload", files={"file": ("x.txt", b"text")}, data={"name": " "}
        ).status_code
        == 422
    )
    assert (
        client.post(
            "/v1/sources/upload", files={"file": ("x.txt", b"text")}, data={"format": "pdf"}
        ).status_code
        == 422
    )
    assert (
        client.post(
            "/v1/sources/upload",
            files={"file": ("x.txt", b"text")},
            headers={"Authorization": "Bearer bad"},
        ).status_code
        == 401
    )
    with sessions() as session:
        assert session.scalar(select(func.count()).select_from(Document)) == 0
