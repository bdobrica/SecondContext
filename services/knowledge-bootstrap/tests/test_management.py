from concurrent.futures import ThreadPoolExecutor
from uuid import UUID, uuid4

import httpx
import pytest
from fastapi.testclient import TestClient
from sqlalchemy import func, select
from sqlalchemy.exc import OperationalError

from knowledge_bootstrap.app import create_app
from knowledge_bootstrap.index import IndexError, SearchIndex
from knowledge_bootstrap.ingestion import process_next
from knowledge_bootstrap.models import Chunk, Document, IngestionJob, Source
from knowledge_bootstrap.pipeline import lock_owner, process_chunk_next


def test_ui_shell_static_assets_and_security_headers(settings):
    with TestClient(create_app(settings)) as client:
        assert client.get("/", follow_redirects=False).headers["location"] == "/knowledge"
        response = client.get("/knowledge")
        assert response.status_code == 200 and "text/html" in response.headers["content-type"]
        assert response.template.name == "knowledge.html"
        assert "Add source" in response.text and "Test retrieval" in response.text
        assert str(settings.max_input_bytes) in response.text
        assert str(settings.web_max_pages) in response.text
        assert "test-owner" not in response.text and "unused:unused" not in response.text
        assert "frame-ancestors 'none'" in response.headers["content-security-policy"]
        assert "'unsafe-inline'" not in response.headers["content-security-policy"]
        assert response.headers["cache-control"] == "no-store"
        assert response.headers["referrer-policy"] == "no-referrer"
        assert "set-cookie" not in response.headers
        for filename in ("knowledge.js", "knowledge.css"):
            asset = client.get("/knowledge/static/" + filename)
            assert asset.status_code == 200
            assert asset.headers["x-content-type-options"] == "nosniff"
        assert client.get("/knowledge/static/missing.js").status_code == 404
        assert client.get("/v1/sources").status_code == 401
        assert client.delete("/v1/sources/" + str(uuid4())).status_code == 401


@pytest.fixture
def cleanup_backend(monkeypatch):
    class CleanupIndex:
        calls = []
        failure = False
        hook = None

        def __init__(self, config, **kwargs):
            self.collection = config.qdrant_collection
            assert kwargs["timeout_seconds"] == config.search_timeout_seconds

        def __enter__(self):
            return self

        def __exit__(self, *args):
            pass

        def delete_filter(self, filter, *, allow_missing):
            assert allow_missing
            self.calls.append((self.collection, filter))
            if self.hook:
                type(self).hook()
            if self.failure:
                raise IndexError("index_timeout", "Never expose a backend credential here")

    monkeypatch.setattr("knowledge_bootstrap.management.SearchIndex", CleanupIndex)
    return CleanupIndex


@pytest.mark.integration
def test_source_summaries_count_canonical_rows_and_latest_failure(client, sessions, db_settings):
    source_id = client.post(
        "/v1/sources",
        json={"kind": "text", "name": "Summary handbook", "text": "# Guide\n\nKeep evidence."},
    ).json()["source"]["id"]
    summary = client.get("/v1/sources/" + source_id).json()
    assert summary["document_count"] == summary["chunk_count"] == 0
    assert summary["latest_stage"] == "pending" and summary["last_error_code"] is None
    assert process_next(sessions, db_settings) and process_chunk_next(sessions, db_settings)
    summary = client.get("/v1/sources/" + source_id).json()
    assert summary["document_count"] == 1 and summary["chunk_count"] >= 1
    assert summary["latest_stage"] == "indexing" and summary["last_error_detail"] is None
    assert "input_text" not in summary and "input_bytes" not in summary
    assert client.get("/v1/sources").json() == [summary]
    assert client.get("/v1/sources?offset=1").json() == []
    assert (
        client.get("/v1/sources", headers={"Authorization": "Bearer test-owner-b-token-456"}).json()
        == []
    )
    assert (
        client.get(
            "/v1/sources/" + source_id,
            headers={"Authorization": "Bearer test-owner-b-token-456"},
        ).status_code
        == 404
    )
    # Malformed JSON fails in the worker, and its stable error appears in list and detail.
    failure = client.post(
        "/v1/sources", json={"kind": "text", "format": "json", "text": "{broken"}
    ).json()["source"]["id"]
    assert process_next(sessions, db_settings)
    summary = client.get("/v1/sources/" + failure).json()
    assert summary["status"] == "failed" and summary["last_error_code"]
    assert summary["last_error_detail"] and summary["latest_stage"] == "parsing"
    client.post("/v1/sources/" + failure + "/refresh")
    summary = client.get("/v1/sources/" + failure).json()
    assert summary["status"] == "pending" and summary["last_error_code"] is None


@pytest.mark.integration
def test_delete_cascades_idempotence_and_owner_isolation(
    client, sessions, db_settings, owners, cleanup_backend
):
    source_id = client.post(
        "/v1/sources", json={"kind": "text", "text": "# Guide\n\nKeep evidence."}
    ).json()["source"]["id"]
    process_next(sessions, db_settings)
    process_chunk_next(sessions, db_settings)
    assert (
        client.delete(
            "/v1/sources/" + source_id,
            headers={"Authorization": "Bearer test-owner-b-token-456"},
        ).status_code
        == 204
    )
    assert client.get("/v1/sources/" + source_id).status_code == 200
    assert not cleanup_backend.calls
    assert client.delete("/v1/sources/" + source_id).status_code == 204
    assert cleanup_backend.calls[0][1] == {
        "must": [
            {"key": "kind", "match": {"value": "knowledge"}},
            {"key": "owner_id", "match": {"value": owners[0]}},
            {"key": "source_id", "match": {"value": source_id}},
        ]
    }
    assert client.get("/v1/sources/" + source_id).status_code == 404
    assert client.get("/v1/sources").json() == []
    for model in (Source, Document, Chunk, IngestionJob):
        with sessions() as session:
            assert (
                session.scalar(
                    select(func.count()).select_from(model).where(model.owner_id == owners[0])
                )
                == 0
            )
    assert client.delete("/v1/sources/" + source_id).status_code == 204
    assert len(cleanup_backend.calls) == 1


@pytest.mark.integration
def test_delete_backend_failure_retains_source_for_retry(client, sessions, cleanup_backend):
    data = client.post("/v1/sources", json={"kind": "text", "text": "Keep me."}).json()
    source_id = data["source"]["id"]
    cleanup_backend.failure = True
    response = client.delete("/v1/sources/" + source_id)
    assert response.status_code == 503
    assert response.json()["error"]["code"] == "deletion_unavailable"
    assert "credential" not in response.text
    assert client.get("/v1/sources/" + source_id).status_code == 200
    assert client.get("/v1/jobs/" + data["job"]["id"]).status_code == 200
    cleanup_backend.failure = False
    assert client.delete("/v1/sources/" + source_id).status_code == 204


@pytest.mark.integration
def test_delete_holds_projection_and_source_locks_during_cleanup(
    client, sessions, owners, cleanup_backend
):
    source_id = client.post("/v1/sources", json={"kind": "text", "text": "Keep me."}).json()[
        "source"
    ]["id"]

    def verify_locks():
        with sessions() as session:
            assert lock_owner(session, owners[0], wait=False) is False
            with pytest.raises(OperationalError) as error:
                session.execute(
                    select(Source).where(Source.id == UUID(source_id)).with_for_update(nowait=True)
                )
            assert error.value.orig.sqlstate == "55P03"

    cleanup_backend.hook = verify_locks
    assert client.delete("/v1/sources/" + source_id).status_code == 204


@pytest.mark.integration
def test_concurrent_delete_is_idempotent(client, cleanup_backend):
    source_id = client.post("/v1/sources", json={"kind": "text", "text": "Keep me."}).json()[
        "source"
    ]["id"]
    with ThreadPoolExecutor(max_workers=2) as pool:
        responses = list(pool.map(lambda _: client.delete("/v1/sources/" + source_id), range(2)))
    assert [response.status_code for response in responses] == [204, 204]
    assert len(cleanup_backend.calls) == 1


@pytest.mark.integration
def test_delete_cleans_previous_collection_and_handles_arbitrary_metadata(
    client, sessions, cleanup_backend
):
    source_id = client.post("/v1/sources", json={"kind": "text", "text": "Keep me."}).json()[
        "source"
    ]["id"]
    with sessions.begin() as session:
        source = session.get(Source, UUID(source_id))
        source.metadata_json = {"index": {"collection": "previous_collection"}}
    assert client.delete("/v1/sources/" + source_id).status_code == 204
    assert {collection for collection, _ in cleanup_backend.calls} == {
        "knowledge_chunks",
        "previous_collection",
    }
    for projection, expected in (
        ("arbitrary caller metadata", 204),
        ({"collection": "../escape"}, 503),
        ({"collection": ["invalid type"]}, 503),
        ({"collection": []}, 204),
    ):
        data = client.post(
            "/v1/sources",
            json={"kind": "text", "text": "Keep me.", "metadata_json": {"index": projection}},
        ).json()
        response = client.delete("/v1/sources/" + data["source"]["id"])
        assert response.status_code == expected


@pytest.mark.parametrize("status", [200, 404, 500])
def test_missing_collection_delete_and_write_acknowledgement(settings, status):
    with SearchIndex(
        settings,
        transport=httpx.MockTransport(
            lambda _: httpx.Response(status, json={"result": {"status": "completed"}})
        ),
    ) as index:
        if status == 500:
            with pytest.raises(IndexError):
                index.delete_filter({"must": []}, allow_missing=True)
        else:
            index.delete_filter({"must": []}, allow_missing=True)
    with SearchIndex(
        settings,
        transport=httpx.MockTransport(
            lambda _: httpx.Response(200, json={"result": {"status": "acknowledged"}})
        ),
    ) as index:
        with pytest.raises(IndexError):
            index.delete_filter({"must": []}, allow_missing=True)
