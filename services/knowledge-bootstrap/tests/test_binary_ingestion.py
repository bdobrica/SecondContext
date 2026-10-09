import hashlib
import time
from pathlib import Path
from uuid import UUID

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import Session

from knowledge_bootstrap.app import create_app
from knowledge_bootstrap.ingestion import process_next
from knowledge_bootstrap.models import Source, Stage
from knowledge_bootstrap.parsers import ParseError
from knowledge_bootstrap.schemas import JobTransition
from knowledge_bootstrap.service import transition_job

pytestmark = pytest.mark.integration
FIXTURES = Path(__file__).parent / "fixtures" / "binary"


@pytest.mark.parametrize("fmt", ["pdf", "docx"])
def test_binary_upload_durable_document_idempotency_refresh_and_ownership(
    client, sessions, db_settings, owners, fmt
):
    data = (FIXTURES / f"handbook.{fmt}").read_bytes()
    files = {"file": (f"../../original.{fmt}", data, "application/octet-stream")}
    headers = {"Idempotency-Key": "binary-original"}
    created = client.post("/v1/sources/upload", files=files, headers=headers)
    assert created.status_code == 202
    source, job = created.json()["source"], created.json()["job"]
    assert source["format"] == fmt
    assert source["source_uri"] == f"../../original.{fmt}"
    assert source["content_hash"] == hashlib.sha256(data).hexdigest()
    assert "input_bytes" not in source and "input_text" not in source
    with sessions() as session:
        stored = session.get(Source, UUID(source["id"]))
        assert stored.input_bytes == data and stored.input_text is None
    replay = client.post("/v1/sources/upload", files=files, headers=headers)
    assert replay.json()["source"]["id"] == source["id"]
    changed = client.post(
        "/v1/sources/upload",
        files={"file": (f"../../original.{fmt}", data + b"changed", "application/octet-stream")},
        headers=headers,
    )
    assert changed.status_code == 409
    # A recreated app can consume the durable upload, without the request or its spooled file.
    restarted = create_app(db_settings)
    assert process_next(restarted.state.sessions, db_settings)
    restarted.state.engine.dispose()
    result = client.get(f"/v1/jobs/{job['id']}").json()
    assert result["status"] == "chunking" and result["documents_processed"] == 1
    assert result["chunks_created"] == 0
    document = client.get(f"/v1/sources/{source['id']}/documents").json()[0]
    assert document["format"] == fmt and "Deployment" in document["text_content"]
    assert document["uri"] == f"urn:knowledge:source:{source['id']}"
    assert document["metadata_json"]["source_uri"] == f"../../original.{fmt}"
    if fmt == "pdf":
        assert document["metadata_json"]["page_count"] == 3
        assert document["metadata_json"]["blocks"][0]["page_start"] == 1
    else:
        assert document["title"] == "Deployment handbook"
    other_headers = {"Authorization": "Bearer test-owner-b-token-456"}
    assert client.get(f"/v1/sources/{source['id']}", headers=other_headers).status_code == 404
    assert client.get(f"/v1/documents/{document['id']}", headers=other_headers).status_code == 404
    assert (
        client.post(f"/v1/sources/{source['id']}/refresh", headers=other_headers).status_code == 404
    )
    with sessions() as session:
        transition_job(
            session,
            owners[0],
            UUID(job["id"]),
            JobTransition(
                status=Stage.FAILED, error_code="downstream_unavailable", error_detail="Retry later"
            ),
        )
    refreshed = client.post(f"/v1/sources/{source['id']}/refresh").json()
    assert refreshed["job"]["id"] != job["id"]
    assert process_next(sessions, db_settings)
    after = client.get(f"/v1/sources/{source['id']}/documents").json()
    assert len(after) == 1 and after[0]["id"] == document["id"]
    assert after[0]["content_hash"] == document["content_hash"]
    assert after[0]["metadata_json"] == document["metadata_json"]


@pytest.mark.parametrize(
    "filename,data,code",
    [
        ("scan.pdf", (FIXTURES / "image-only.pdf").read_bytes(), "pdf_text_unavailable"),
        ("broken.pdf", b"%PDF-1.7\nSECRET corrupt", "invalid_pdf"),
        ("broken.docx", b"PK\x03\x04SECRET corrupt", "invalid_docx"),
    ],
)
def test_binary_failure_is_durable_and_retryable(
    client, sessions, db_settings, filename, data, code
):
    response = client.post("/v1/sources/upload", files={"file": (filename, data)})
    assert response.status_code == 202
    source_id, job_id = response.json()["source"]["id"], response.json()["job"]["id"]
    assert process_next(sessions, db_settings)
    job = client.get(f"/v1/jobs/{job_id}").json()
    assert job["status"] == "failed" and job["stage"] == "parsing"
    assert job["error_code"] == code and "SECRET" not in job["error_detail"]
    assert job["documents_processed"] == 0
    assert client.get(f"/v1/sources/{source_id}/documents").json() == []
    retry = client.post(f"/v1/sources/{source_id}/refresh").json()
    assert retry["job"]["id"] != job_id
    assert process_next(sessions, db_settings)
    assert client.get(f"/v1/jobs/{retry['job']['id']}").json()["error_code"] == code
    assert client.get(f"/v1/jobs/{job_id}").json() == job


def test_binary_upload_validation_limits_and_auth(client, db_settings):
    path = FIXTURES / "handbook.pdf"
    assert (
        client.post("/v1/sources/upload", files={"file": ("fake.pdf", b"text")}).json()["error"][
            "code"
        ]
        == "file_type_mismatch"
    )
    assert (
        client.post(
            "/v1/sources/upload", files={"file": ("fake.docx", path.read_bytes())}
        ).status_code
        == 422
    )
    assert (
        client.post(
            "/v1/sources/upload",
            files={"file": ("x.pdf", path.read_bytes())},
            headers={"Authorization": "Bearer wrong"},
        ).status_code
        == 401
    )
    settings = db_settings.model_copy(update={"max_file_bytes": 100})
    app = create_app(settings)
    with TestClient(app) as limited:
        limited.headers["Authorization"] = "Bearer test-owner-a-token-123"
        response = limited.post("/v1/sources/upload", files={"file": ("x.pdf", path.read_bytes())})
        assert response.status_code == 413 and response.json()["error"]["code"] == "input_too_large"


def test_parser_failure_does_not_stop_worker_or_create_partial_document(
    client, sessions, db_settings, monkeypatch
):
    from knowledge_bootstrap import ingestion

    binary = client.post(
        "/v1/sources/upload", files={"file": ("x.pdf", (FIXTURES / "handbook.pdf").read_bytes())}
    ).json()
    text = client.post("/v1/sources", json={"kind": "text", "text": "Continue processing."}).json()

    def timeout(*args):
        raise ParseError("parser_timeout", "Document parser exceeded its time limit")

    monkeypatch.setattr(ingestion, "parse_binary", timeout)
    assert process_next(sessions, db_settings)
    assert client.get(f"/v1/jobs/{binary['job']['id']}").json()["error_code"] == "parser_timeout"
    assert process_next(sessions, db_settings)
    assert client.get(f"/v1/jobs/{text['job']['id']}").json()["status"] == "chunking"


def test_binary_transaction_rollback_keeps_original_input(
    client, sessions, db_settings, monkeypatch
):
    created = client.post(
        "/v1/sources/upload", files={"file": ("x.docx", (FIXTURES / "handbook.docx").read_bytes())}
    ).json()
    flush = Session.flush

    def crash(session, *args, **kwargs):
        raise RuntimeError("Injected persistence failure")

    monkeypatch.setattr(Session, "flush", crash)
    with pytest.raises(RuntimeError):
        process_next(sessions, db_settings)
    monkeypatch.setattr(Session, "flush", flush)
    assert client.get(f"/v1/jobs/{created['job']['id']}").json()["status"] == "pending"
    assert client.get(f"/v1/sources/{created['source']['id']}/documents").json() == []
    with sessions() as session:
        assert session.get(Source, UUID(created["source"]["id"])).input_bytes is not None
    assert process_next(sessions, db_settings)


def test_running_worker_consumes_persisted_binary(client, sessions, db_settings):
    created = client.post(
        "/v1/sources/upload", files={"file": ("x.pdf", (FIXTURES / "handbook.pdf").read_bytes())}
    ).json()
    settings = db_settings.model_copy(
        update={"text_worker_enabled": True, "worker_poll_seconds": 0.05}
    )
    with TestClient(create_app(settings)):
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            state = client.get(f"/v1/jobs/{created['job']['id']}").json()
            # K5 continues immediately into chunking/indexing; polling may miss the
            # transient chunking stage even though binary parsing completed correctly.
            if state["status"] in {"chunking", "indexing"}:
                assert state["documents_found"] == state["documents_processed"] == 1
                break
            assert state["status"] != "failed", state.get("error_code")
            time.sleep(0.05)
        else:
            pytest.fail("Worker did not finish binary parsing")
    with sessions() as session:
        assert (
            session.scalar(
                select(Source).where(Source.id == UUID(created["source"]["id"]))
            ).input_bytes
            is not None
        )
