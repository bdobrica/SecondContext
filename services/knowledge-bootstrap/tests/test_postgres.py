from concurrent.futures import ThreadPoolExecutor
from uuid import UUID, uuid4

import pytest
from alembic import command
from alembic.config import Config
from sqlalchemy import func, inspect, select, text
from sqlalchemy.exc import IntegrityError

from knowledge_bootstrap.models import Base, Chunk, Document, IngestionJob, Source, Stage
from knowledge_bootstrap.schemas import JobTransition, SourceCreate
from knowledge_bootstrap.service import (
    ServiceError,
    create_source,
    refresh_source,
    transition_job,
)

from .conftest import ROOT

pytestmark = pytest.mark.integration
PAYLOAD = {
    "kind": "text",
    "name": "Handbook",
    "format": "markdown",
    "text": "# Deployments\nPolicy",
}
OTHER_TOKEN = {"Authorization": "Bearer test-owner-b-token-456"}


def test_api_source_job_and_owner_isolation(client):
    response = client.post("/v1/sources", json=PAYLOAD)
    assert response.status_code == 202
    created = response.json()
    source_id, job_id = created["source"]["id"], created["job"]["id"]
    assert created["source"]["status"] == created["job"]["status"] == "pending"
    assert created["job"]["source_id"] == source_id
    assert "input_text" not in created["source"]
    assert client.get("/readyz").status_code == 200
    assert len(client.get("/v1/sources").json()) == 1
    assert client.get(f"/v1/sources/{source_id}").status_code == 200
    assert client.get(f"/v1/jobs/{job_id}").status_code == 200
    assert client.get(f"/v1/sources/{source_id}/jobs").json()[0]["id"] == job_id
    assert client.get(f"/v1/sources/{source_id}/documents").json() == []
    assert client.get("/v1/sources", headers=OTHER_TOKEN).json() == []
    for path in [
        f"/v1/sources/{source_id}",
        f"/v1/jobs/{job_id}",
        f"/v1/sources/{source_id}/jobs",
        f"/v1/sources/{source_id}/documents",
    ]:
        assert client.get(path, headers=OTHER_TOKEN).status_code == 404
    assert client.post(f"/v1/sources/{source_id}/refresh", headers=OTHER_TOKEN).status_code == 404
    assert client.get(f"/v1/sources/{uuid4()}").status_code == 404
    assert client.get("/v1/sources/not-a-uuid").status_code == 422
    assert client.get("/v1/sources?limit=101").status_code == 422
    assert client.post("/v1/sources", json=PAYLOAD | {"owner_id": "spoofed"}).status_code == 422


def test_creation_idempotency_scoped_to_owner(client):
    headers = {"Idempotency-Key": "client-request-1"}
    first = client.post("/v1/sources", json=PAYLOAD, headers=headers).json()
    replay = client.post("/v1/sources", json=PAYLOAD, headers=headers).json()
    assert first == replay
    conflict = client.post("/v1/sources", json=PAYLOAD | {"name": "Different"}, headers=headers)
    assert conflict.status_code == 409
    assert conflict.json()["error"]["code"] == "idempotency_conflict"
    other = client.post("/v1/sources", json=PAYLOAD, headers=headers | OTHER_TOKEN)
    assert other.status_code == 202
    assert other.json()["source"]["id"] != first["source"]["id"]
    assert (
        client.post("/v1/sources", json=PAYLOAD, headers={"Idempotency-Key": " "}).status_code
        == 422
    )


def test_creation_replay_keeps_original_job_after_refresh(client, sessions, owners):
    headers = {"Idempotency-Key": "original-job"}
    created = client.post("/v1/sources", json=PAYLOAD, headers=headers).json()
    job_id = UUID(created["job"]["id"])
    with sessions() as session:
        transition_job(
            session,
            owners[0],
            job_id,
            JobTransition(status=Stage.FAILED, error_code="test_failure", error_detail="Retry"),
        )
    refreshed = client.post(f"/v1/sources/{created['source']['id']}/refresh").json()
    assert refreshed["job"]["id"] != str(job_id)
    replay = client.post("/v1/sources", json=PAYLOAD, headers=headers).json()
    assert replay["job"]["id"] == str(job_id)
    assert replay["job"]["status"] == "failed"
    assert replay["source"]["status"] == "pending"


def test_durable_transitions_failure_and_retry(client, sessions, owners):
    created = client.post("/v1/sources", json=PAYLOAD).json()
    job_id, source_id = UUID(created["job"]["id"]), UUID(created["source"]["id"])
    with sessions() as session:
        with pytest.raises(ServiceError) as error:
            transition_job(session, owners[1], job_id, JobTransition(status=Stage.PARSING))
        assert error.value.status_code == 404
    for status in [Stage.PARSING, Stage.CHUNKING]:
        with sessions() as session:
            transition_job(
                session, owners[0], job_id, JobTransition(status=status, documents_found=2)
            )
    with sessions() as session:
        transition_job(
            session,
            owners[0],
            job_id,
            JobTransition(status=Stage.FAILED, error_code="index_failed", error_detail="Try again"),
        )
    job = client.get(f"/v1/jobs/{job_id}").json()
    assert job["status"] == "failed" and job["stage"] == "chunking"
    assert job["started_at"] and job["finished_at"] and job["error_code"] == "index_failed"
    retry = client.post(f"/v1/sources/{source_id}/refresh").json()
    retry_id = retry["job"]["id"]
    assert retry_id != str(job_id)
    assert retry["job"]["documents_found"] == 0
    assert client.post(f"/v1/sources/{source_id}/refresh").json()["job"]["id"] == retry_id
    for status in [Stage.PARSING, Stage.CHUNKING, Stage.INDEXING, Stage.READY]:
        with sessions() as session:
            transition_job(session, owners[0], UUID(retry_id), JobTransition(status=status))
    ready = client.get(f"/v1/sources/{source_id}").json()
    assert ready["status"] == "ready" and ready["last_ingested_at"]
    assert client.get(f"/v1/jobs/{job_id}").json() == job


def test_atomic_source_and_job_creation(sessions, owners, monkeypatch):
    with sessions() as session:
        original = session.flush
        calls = 0

        def failing_flush(*args, **kwargs):
            nonlocal calls
            calls += 1
            if calls == 2:
                raise RuntimeError("simulated job persistence failure")
            return original(*args, **kwargs)

        monkeypatch.setattr(session, "flush", failing_flush)
        with pytest.raises(RuntimeError, match="persistence failure"):
            create_source(session, owners[0], SourceCreate(**PAYLOAD))
    with sessions() as session:
        assert (
            session.scalar(
                select(func.count()).select_from(Source).where(Source.owner_id == owners[0])
            )
            == 0
        )


def test_concurrent_creation_and_refresh_do_not_duplicate_jobs(sessions, owners):
    def create(_):
        with sessions() as session:
            return create_source(session, owners[0], SourceCreate(**PAYLOAD), "concurrent")[0].id

    with ThreadPoolExecutor(max_workers=8) as executor:
        source_ids = set(executor.map(create, range(8)))
    assert len(source_ids) == 1
    source_id = source_ids.pop()
    # Finish the original so concurrent refresh actually exercises insertion, not just reuse.
    with sessions() as session:
        job_id = session.scalar(select(IngestionJob.id).where(IngestionJob.source_id == source_id))
    with sessions() as session:
        transition_job(
            session,
            owners[0],
            job_id,
            JobTransition(status=Stage.FAILED, error_code="test_failure", error_detail="Retry me"),
        )

    def refresh(_):
        with sessions() as session:
            return refresh_source(session, owners[0], source_id)[1].id

    with ThreadPoolExecutor(max_workers=8) as executor:
        assert len(set(executor.map(refresh, range(8)))) == 1
    with sessions() as session:
        assert (
            session.scalar(
                select(func.count())
                .select_from(IngestionJob)
                .where(IngestionJob.source_id == source_id)
            )
            == 2
        )


def seed_document(sessions, owner):
    with sessions() as session:
        source, _ = create_source(session, owner, SourceCreate(**PAYLOAD))
    with sessions.begin() as session:
        document = Document(
            source_id=source.id,
            owner_id=owner,
            uri="handbook.md",
            title="Handbook",
            format="markdown",
            text_content="Policy",
            content_hash="a" * 64,
        )
        session.add(document)
        session.flush()
        chunk = Chunk(
            document_id=document.id,
            source_id=source.id,
            owner_id=owner,
            ordinal=0,
            text="Policy",
            heading_path=["Deployments"],
            page_start=1,
            page_end=2,
            token_count=1,
            content_hash="b" * 64,
        )
        session.add(chunk)
        session.flush()
    return source, document, chunk


def test_document_chunk_inspection_and_database_owner_constraints(client, sessions, owners):
    source, document, chunk = seed_document(sessions, owners[0])
    assert client.get(f"/v1/sources/{source.id}/documents").json()[0]["id"] == str(document.id)
    assert client.get(f"/v1/documents/{document.id}").json()["title"] == "Handbook"
    result = client.get(f"/v1/documents/{document.id}/chunks").json()[0]
    assert result["id"] == str(chunk.id)
    assert result["heading_path"] == ["Deployments"] and result["page_end"] == 2
    for path in [f"/v1/documents/{document.id}", f"/v1/documents/{document.id}/chunks"]:
        assert client.get(path, headers=OTHER_TOKEN).status_code == 404
    with sessions() as session:
        with pytest.raises(IntegrityError), session.begin():
            session.add(
                Document(
                    source_id=source.id,
                    owner_id=owners[1],
                    uri="illegal",
                    title="Illegal",
                    text_content="bad",
                    content_hash="c" * 64,
                )
            )
            session.flush()
    with sessions() as session:
        with pytest.raises(IntegrityError), session.begin():
            session.add(
                Chunk(
                    document_id=document.id,
                    source_id=source.id,
                    owner_id=owners[1],
                    ordinal=1,
                    text="bad",
                    token_count=1,
                    content_hash="c" * 64,
                )
            )
            session.flush()


def test_database_rejects_invalid_provenance_and_duplicate_active_jobs(sessions, owners):
    source, document, _ = seed_document(sessions, owners[0])
    with sessions() as session:
        with pytest.raises(IntegrityError), session.begin():
            session.add(IngestionJob(owner_id=owners[0], source_id=source.id))
            session.flush()
    with sessions() as session:
        with pytest.raises(IntegrityError), session.begin():
            session.add(
                Chunk(
                    document_id=document.id,
                    source_id=source.id,
                    owner_id=owners[0],
                    ordinal=1,
                    text="bad",
                    token_count=1,
                    page_start=3,
                    page_end=2,
                    content_hash="c" * 64,
                )
            )
            session.flush()


def test_migration_upgrade_downgrade_and_model_parity(sessions):
    schema = "migration_test_" + uuid4().hex
    engine = sessions.kw["bind"]
    with engine.begin() as connection:
        connection.execute(text(f'CREATE SCHEMA "{schema}"'))
        connection.execute(text(f'SET LOCAL search_path TO "{schema}"'))
        config = Config(str(ROOT / "alembic.ini"))
        config.attributes["connection"] = connection
        command.upgrade(config, "head")
        assert set(inspect(connection).get_table_names(schema=schema)) == {
            "alembic_version",
            *Base.metadata.tables,
        }
        # Autogeneration catches migration/ORM drift rather than merely checking table names.
        command.check(config)
        command.downgrade(config, "base")
        assert inspect(connection).get_table_names(schema=schema) == ["alembic_version"]
        command.upgrade(config, "head")
        connection.execute(text(f'DROP SCHEMA "{schema}" CASCADE'))


def test_binary_migration_preserves_text_and_enforces_input_invariants(sessions):
    schema = "binary_migration_test_" + uuid4().hex
    engine = sessions.kw["bind"]
    with engine.begin() as connection:
        connection.execute(text(f'CREATE SCHEMA "{schema}"'))
        connection.execute(text(f'SET LOCAL search_path TO "{schema}"'))
        config = Config(str(ROOT / "alembic.ini"))
        config.attributes["connection"] = connection
        command.upgrade(config, "0001_knowledge_core")
        source_id = uuid4()
        connection.execute(
            text(
                "INSERT INTO knowledge_sources (id, owner_id, kind, name, input_text, status) "
                "VALUES (:id, 'existing-owner', 'text', 'Existing source', "
                "'Keep this text', 'pending')"
            ),
            {"id": source_id},
        )
        command.upgrade(config, "head")
        assert connection.execute(
            text("SELECT input_text, input_bytes FROM knowledge_sources WHERE id = :id"),
            {"id": source_id},
        ).one() == ("Keep this text", None)
        insert = text(
            "INSERT INTO knowledge_sources "
            "(id, owner_id, kind, name, format, input_text, input_bytes, status) "
            "VALUES (:id, 'owner', :kind, 'Upload', :format, :text, :bytes, 'pending')"
        )
        for kind, fmt, input_text in [
            ("file", None, None),
            ("file", "text", None),
            ("text", "pdf", None),
            ("file", "pdf", "text"),
        ]:
            with pytest.raises(IntegrityError), connection.begin_nested():
                connection.execute(
                    insert,
                    {
                        "id": uuid4(),
                        "kind": kind,
                        "format": fmt,
                        "text": input_text,
                        "bytes": b"binary",
                    },
                )
        connection.execute(
            insert,
            {"id": uuid4(), "kind": "file", "format": "pdf", "text": None, "bytes": b"binary"},
        )
        command.downgrade(config, "0001_knowledge_core")
        assert (
            connection.scalar(
                text("SELECT input_text FROM knowledge_sources WHERE id = :id"), {"id": source_id}
            )
            == "Keep this text"
        )
        connection.execute(text(f'DROP SCHEMA "{schema}" CASCADE'))
