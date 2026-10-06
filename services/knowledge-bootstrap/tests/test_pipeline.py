from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from uuid import UUID, uuid4

import pytest
from sqlalchemy import delete, select
from sqlalchemy.orm import Session

from knowledge_bootstrap.index import IndexError
from knowledge_bootstrap.ingestion import process_next
from knowledge_bootstrap.models import Chunk, IngestionJob, Source, Stage
from knowledge_bootstrap.pipeline import process_chunk_next, process_index_next, rebuild_index

pytestmark = pytest.mark.integration


class FakeIndex:
    points = {}
    fail_after = None
    writes = 0
    calls = []

    def __init__(self, settings):
        pass

    def __enter__(self):
        return self

    def __exit__(self, *args):
        pass

    def ensure_collection(self):
        self.calls.append("ensure")

    def embed(self, inputs):
        return [[1.0, 2.0, 3.0] for _ in inputs]

    def upsert(self, points):
        type(self).writes += 1
        for p in points:
            self.points[p["id"]] = p
        if self.fail_after == self.writes:
            raise IndexError("index_write_failed", "simulated partial write")

    def delete_filter(self, filter):
        self.calls.append(filter)

        def match(p, condition):
            v = p["payload"].get(condition["key"])
            expected = condition["match"]
            return v in expected["any"] if "any" in expected else v == expected["value"]

        for key, p in list(self.points.items()):
            if all(match(p, c) for c in filter["must"]) and not any(
                match(p, c) for c in filter.get("must_not", [])
            ):
                del self.points[key]


@pytest.fixture
def fake_index():
    FakeIndex.points, FakeIndex.calls, FakeIndex.writes, FakeIndex.fail_after = {}, [], 0, None
    return FakeIndex


@pytest.fixture
def config(db_settings):
    return db_settings.model_copy(
        update={
            "indexing_enabled": True,
            "embedding_dimensions": 3,
            "qdrant_collection": "knowledge_test_" + uuid4().hex,
            "index_batch_size": 1,
        }
    )


def create_and_chunk(
    client, sessions, config, text="# Handbook\n\nSafe deployments.\n\n## Backup\nRetain copies."
):
    response = client.post("/v1/sources", json={"kind": "text", "text": text, "format": "markdown"})
    assert response.status_code == 202
    created = response.json()
    assert process_next(sessions, config)
    assert process_chunk_next(sessions, config)
    return created["source"]["id"], created["job"]["id"]


def snapshot(sessions, source_id):
    with sessions() as s:
        return [
            (str(c.id), c.text, c.content_hash, c.ordinal, c.metadata_json)
            for c in s.scalars(
                select(Chunk).where(Chunk.source_id == UUID(source_id)).order_by(Chunk.ordinal)
            )
        ]


def test_canonical_first_ready_payload_and_owner_isolation(
    client, sessions, config, owners, fake_index
):
    source_id, job_id = create_and_chunk(client, sessions, config)
    before = snapshot(sessions, source_id)
    assert len(before) == 2
    assert not fake_index.points
    job = client.get("/v1/jobs/" + job_id).json()
    assert job["stage"] == "indexing" and job["chunks_created"] == 2
    doc = client.get(f"/v1/sources/{source_id}/documents").json()[0]
    chunks = client.get(f"/v1/documents/{doc['id']}/chunks").json()
    assert chunks[0]["heading_path"] == ["Handbook"]
    assert chunks[0]["metadata_json"]["blocks"][0]["type"] == "heading"
    assert doc["metadata_json"]["uri"] == doc["uri"]
    assert (
        client.get(
            f"/v1/documents/{doc['id']}/chunks",
            headers={"Authorization": "Bearer test-owner-b-token-456"},
        ).status_code
        == 404
    )
    assert (
        client.post(
            f"/v1/sources/{source_id}/reindex",
            headers={"Authorization": "Bearer test-owner-b-token-456"},
        ).status_code
        == 404
    )
    assert process_index_next(sessions, config, fake_index)
    assert not process_index_next(sessions, config, fake_index)
    job = client.get("/v1/jobs/" + job_id).json()
    assert job["status"] == "ready" and job["finished_at"]
    assert client.get("/v1/sources/" + source_id).json()["last_ingested_at"]
    assert snapshot(sessions, source_id) == before
    for point in fake_index.points.values():
        p = point["payload"]
        assert p["owner_id"] == owners[0] and p["source_id"] == source_id
        assert p["document_id"] == doc["id"] and p["chunk_id"] == point["id"]
        assert p["title"] == "Handbook" and p["url"] == doc["uri"] and p["format"] == "markdown"
        assert point["vector"]["dense"] == [1, 2, 3]
        assert point["vector"]["sparse"]["indices"]
        assert "text" not in p


def test_partial_index_failure_retry_without_reparse_or_duplicate_chunks(
    client, sessions, config, fake_index, monkeypatch
):
    source_id, job_id = create_and_chunk(client, sessions, config)
    before = snapshot(sessions, source_id)
    fake_index.fail_after = 1
    assert process_index_next(sessions, config, fake_index)
    failed = client.get("/v1/jobs/" + job_id).json()
    assert failed["status"] == "failed" and failed["stage"] == "indexing"
    assert failed["error_code"] == "index_write_failed" and len(fake_index.points) == 1
    assert snapshot(sessions, source_id) == before
    retry = client.post(f"/v1/sources/{source_id}/reindex").json()
    assert retry["job"]["id"] != job_id and retry["job"]["stage"] == "indexing"
    assert client.post(f"/v1/sources/{source_id}/reindex").json()["job"]["id"] == retry["job"]["id"]
    monkeypatch.setattr(
        "knowledge_bootstrap.ingestion.parse_text", lambda *a: pytest.fail("reparsed")
    )
    fake_index.fail_after = None
    assert process_index_next(sessions, config, fake_index)
    assert len(fake_index.points) == len(before) == 2
    assert snapshot(sessions, source_id) == before
    assert client.get("/v1/jobs/" + job_id).json() == failed
    assert client.get("/v1/jobs/" + retry["job"]["id"]).json()["status"] == "ready"


def test_refresh_stable_ids_and_stale_points_owner_safe(
    client, sessions, config, owners, fake_index
):
    source_id, job_id = create_and_chunk(client, sessions, config)
    process_index_next(sessions, config, fake_index)
    before = snapshot(sessions, source_id)
    client.post(f"/v1/sources/{source_id}/refresh")
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    assert snapshot(sessions, source_id) == before
    process_index_next(sessions, config, fake_index)
    unrelated = str(uuid4())
    fake_index.points[unrelated] = {
        "payload": {"kind": "knowledge", "owner_id": owners[1], "source_id": source_id}
    }
    with sessions.begin() as s:
        s.get(Source, UUID(source_id)).input_text = "# Handbook\n\nChanged content."
    client.post(f"/v1/sources/{source_id}/refresh")
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    current = snapshot(sessions, source_id)
    assert len(current) == 1 and current[0][0] not in {row[0] for row in before}
    process_index_next(sessions, config, fake_index)
    assert set(fake_index.points) == {current[0][0], unrelated}


def test_external_success_database_rollback_is_replayable(
    client, sessions, config, fake_index, monkeypatch
):
    source_id, job_id = create_and_chunk(client, sessions, config)
    original = Session.flush

    def fail_ready(session, *args, **kwargs):
        if any(
            isinstance(row, IngestionJob) and row.status == Stage.READY for row in session.dirty
        ):
            raise RuntimeError("simulated database failure after external success")
        return original(session, *args, **kwargs)

    with monkeypatch.context() as patch:
        patch.setattr(Session, "flush", fail_ready)
        with pytest.raises(RuntimeError):
            process_index_next(sessions, config, fake_index)
    assert client.get("/v1/jobs/" + job_id).json()["status"] == "indexing"
    assert len(fake_index.points) == 2
    assert process_index_next(sessions, config, fake_index)
    assert len(fake_index.points) == 2
    assert client.get("/v1/jobs/" + job_id).json()["status"] == "ready"


def test_rebuild_from_postgres_and_orphan_cleanup(client, sessions, config, owners, fake_index):
    source_id, job_id = create_and_chunk(client, sessions, config)
    process_index_next(sessions, config, fake_index)
    expected = set(fake_index.points)
    fake_index.points.clear()
    orphan, unrelated = str(uuid4()), str(uuid4())
    fake_index.points[orphan] = {
        "payload": {"kind": "knowledge", "owner_id": owners[0], "source_id": str(uuid4())}
    }
    fake_index.points[unrelated] = {
        "payload": {"kind": "knowledge", "owner_id": owners[1], "source_id": str(uuid4())}
    }
    jobs = rebuild_index(sessions, config, owners[0], fake_index)
    assert len(jobs) == 1 and jobs[0].status == Stage.READY
    assert set(fake_index.points) == expected | {unrelated}
    assert jobs[0].id != UUID(job_id)
    # Canonical deletion can be reconciled even when no sources remain.
    with sessions.begin() as s:
        s.execute(delete(Source).where(Source.id == UUID(source_id)))
    assert rebuild_index(sessions, config, owners[0], fake_index) == []
    assert set(fake_index.points) == {unrelated}


def test_recipe_change_requires_new_collection(client, sessions, config, fake_index):
    source_id, job_id = create_and_chunk(client, sessions, config)
    process_index_next(sessions, config, fake_index)
    retry = client.post(f"/v1/sources/{source_id}/reindex").json()["job"]
    changed = config.model_copy(update={"embedding_model": "another-model"})
    process_index_next(sessions, changed, fake_index)
    failure = client.get("/v1/jobs/" + retry["id"]).json()
    assert failure["error_code"] == "index_configuration_mismatch"


def test_pipeline_concurrency_ownership_and_disabled_index(
    client, sessions, config, owners, fake_index
):
    source_id, job_id = create_and_chunk(client, sessions, config)
    other = config.model_copy(update={"auth_tokens": {owners[1]: config.auth_tokens[owners[1]]}})
    assert not process_index_next(sessions, other, fake_index)
    assert not process_index_next(
        sessions, config.model_copy(update={"indexing_enabled": False}), fake_index
    )
    with ThreadPoolExecutor(max_workers=6) as executor:
        results = list(
            executor.map(lambda _: process_index_next(sessions, config, fake_index), range(6))
        )
    assert sum(results) == 1 and len(fake_index.points) == 2


def test_chunking_rollback_and_limit_failure(client, sessions, config, monkeypatch):
    created = client.post("/v1/sources", json={"kind": "text", "text": "# A\nX\n# B\nY"}).json()
    process_next(sessions, config)
    original = Session.flush

    def fail_chunks(session, *args, **kwargs):
        if any(isinstance(row, Chunk) for row in session.new):
            raise RuntimeError("simulated chunk persistence interruption")
        return original(session, *args, **kwargs)

    with monkeypatch.context() as patch:
        patch.setattr(Session, "flush", fail_chunks)
        with pytest.raises(RuntimeError):
            process_chunk_next(sessions, config)
    assert client.get("/v1/jobs/" + created["job"]["id"]).json()["status"] == "chunking"
    assert snapshot(sessions, created["source"]["id"]) == []
    process_chunk_next(sessions, config.model_copy(update={"max_chunks_per_source": 1}))
    job = client.get("/v1/jobs/" + created["job"]["id"]).json()
    assert job["status"] == "failed" and job["stage"] == "chunking"
    assert job["error_code"] == "chunk_limit_exceeded"
    assert snapshot(sessions, created["source"]["id"]) == []


@pytest.mark.parametrize(
    "format,text",
    [
        ("text", "Paragraph one.\n\nParagraph two."),
        ("markdown", "# Policy\n\nParagraph.\n\n- list\n\n```py\nx = 1\n```"),
        ("json", '{"rules":{"deploy":true}}'),
        ("yaml", "rules:\n  deploy: true"),
    ],
)
def test_all_text_formats_produce_ready_inspectable_chunks(
    client, sessions, config, fake_index, format, text
):
    created = client.post(
        "/v1/sources", json={"kind": "text", "text": text, "format": format}
    ).json()
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    process_index_next(sessions, config, fake_index)
    assert client.get("/v1/jobs/" + created["job"]["id"]).json()["status"] == "ready"
    chunks = snapshot(sessions, created["source"]["id"])
    assert chunks
    if format in {"json", "yaml"}:
        assert any(b["path"] == "/rules/deploy" for c in chunks for b in c[4]["blocks"])


@pytest.mark.parametrize("format", ["pdf", "docx"])
def test_binary_formats_ready_and_provenance(client, sessions, config, fake_index, format):
    path = Path(__file__).parent / "fixtures" / "binary" / ("handbook." + format)
    if not path.exists():
        path = next((Path(__file__).parent / "fixtures" / "binary").glob("*." + format))
    created = client.post(
        "/v1/sources/upload", files={"file": ("example." + format, path.read_bytes())}
    ).json()
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    process_index_next(sessions, config, fake_index)
    assert client.get("/v1/jobs/" + created["job"]["id"]).json()["status"] == "ready"
    with sessions() as s:
        chunks = s.scalars(
            select(Chunk).where(Chunk.source_id == UUID(created["source"]["id"]))
        ).all()
        assert chunks
        if format == "pdf":
            assert all(c.page_start is not None for c in chunks)
        else:
            assert any(c.heading_path for c in chunks)


def test_html_ready_final_url_heading_provenance(client, sessions, config, fake_index, monkeypatch):
    from knowledge_bootstrap.web_crawl import CrawlResult
    from knowledge_bootstrap.web_fetch import FetchResult
    from knowledge_bootstrap.web_html import parse_html

    fixture = (Path(__file__).parent / "fixtures" / "web" / "handbook.html").read_bytes()
    response = FetchResult(
        "https://example.com/docs",
        "https://example.com/docs/",
        200,
        "text/html; charset=utf-8",
        fixture,
        "2026-10-06T00:00:00+00:00",
        [],
    )
    parsed, _ = parse_html(response, config)
    monkeypatch.setattr(
        "knowledge_bootstrap.ingestion.ingest_website",
        lambda *a: CrawlResult([parsed], {"documents": 1}),
    )
    created = client.post(
        "/v1/sources", json={"kind": "url", "source_uri": response.requested_url}
    ).json()
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    process_index_next(sessions, config, fake_index)
    assert client.get("/v1/jobs/" + created["job"]["id"]).json()["status"] == "ready"
    assert all(p["payload"]["url"] == response.final_url for p in fake_index.points.values())
    assert any(p["payload"]["heading_path"] for p in fake_index.points.values())


def test_reindex_without_chunks_and_pending_reuse(client):
    created = client.post("/v1/sources", json={"kind": "text", "text": "Policy"}).json()
    assert (
        client.post("/v1/sources/" + created["source"]["id"] + "/reindex").json()["job"]["id"]
        == created["job"]["id"]
    )


def test_single_connection_pool_projection_and_manifest_survive_rollback(
    client, sessions, config, fake_index, monkeypatch
):
    from knowledge_bootstrap.database import make_engine, make_sessions
    from knowledge_bootstrap.models import IndexConfiguration

    source_id, job_id = create_and_chunk(client, sessions, config)
    one = config.model_copy(update={"database_pool_size": 1})
    engine = make_engine(one)
    factory = make_sessions(engine)
    original = Session.flush

    def fail_ready(session, *args, **kwargs):
        if any(
            isinstance(row, IngestionJob) and row.status == Stage.READY for row in session.dirty
        ):
            raise RuntimeError("after external success")
        return original(session, *args, **kwargs)

    try:
        with monkeypatch.context() as patch:
            patch.setattr(Session, "flush", fail_ready)
            with pytest.raises(RuntimeError):
                process_index_next(factory, one, fake_index)
        with factory() as s:
            assert s.scalar(
                select(IndexConfiguration).where(
                    IndexConfiguration.config_json["embedding_model"].astext == one.embedding_model
                )
            )
        assert process_index_next(factory, one, fake_index)
        assert client.get("/v1/jobs/" + job_id).json()["status"] == "ready"
    finally:
        engine.dispose()


def test_failed_refresh_keeps_old_points_until_successful_retry(
    client, sessions, config, fake_index
):
    source_id, job_id = create_and_chunk(client, sessions, config)
    process_index_next(sessions, config, fake_index)
    previous = set(fake_index.points)
    with sessions.begin() as s:
        s.get(Source, UUID(source_id)).input_text = "# Changed\nNew content."
    client.post(f"/v1/sources/{source_id}/refresh")
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    fake_index.fail_after = fake_index.writes + 1
    process_index_next(sessions, config, fake_index)
    assert previous <= set(fake_index.points)
    client.post(f"/v1/sources/{source_id}/reindex")
    fake_index.fail_after = None
    process_index_next(sessions, config, fake_index)
    assert set(fake_index.points).isdisjoint(previous)
    assert len(fake_index.points) == 1
