import os
from uuid import UUID, uuid4

import httpx
import pytest
from fastapi.testclient import TestClient
from sqlalchemy import select

from knowledge_bootstrap.app import create_app
from knowledge_bootstrap.index import SearchIndex, owned_filter, sparse_vector
from knowledge_bootstrap.ingestion import process_next
from knowledge_bootstrap.models import Chunk
from knowledge_bootstrap.pipeline import process_chunk_next, process_index_next, rebuild_index

pytestmark = pytest.mark.integration


class DeterministicEmbeddingIndex(SearchIndex):
    # Isolate real Qdrant operations from paid/external inference in the test suite.
    def embed(self, texts):
        return [[1.0, 0.1, 0.2] for _ in texts]


def test_real_qdrant_projection_dense_sparse_stale_cleanup_and_rebuild(
    client, sessions, db_settings, owners, request, monkeypatch
):
    url = os.environ.get("KNOWLEDGE_TEST_QDRANT_URL")
    if not url:
        if request.config.getoption("--require-qdrant"):
            pytest.fail("KNOWLEDGE_TEST_QDRANT_URL is required")
        pytest.skip("set KNOWLEDGE_TEST_QDRANT_URL to run real Qdrant projection tests")
    name = "knowledge_integration_" + uuid4().hex
    config = db_settings.model_copy(
        update={
            "indexing_enabled": True,
            "embedding_dimensions": 3,
            "qdrant_url": url,
            "qdrant_collection": name,
        }
    )
    path = url + "/collections/" + name
    with httpx.Client(trust_env=False, timeout=10) as q:
        try:
            created = client.post(
                "/v1/sources",
                json={
                    "kind": "text",
                    "format": "markdown",
                    "text": "# Handbook\nDeploy safely.\n\n## Backup\nRestore backups.",
                },
            ).json()
            source_id, job_id = created["source"]["id"], created["job"]["id"]
            assert process_next(sessions, config)
            assert process_chunk_next(sessions, config)
            assert process_index_next(sessions, config, DeterministicEmbeddingIndex)
            assert client.get("/v1/jobs/" + job_id).json()["status"] == "ready"
            with sessions() as s:
                canonical = {
                    str(i)
                    for i in s.scalars(select(Chunk.id).where(Chunk.source_id == UUID(source_id)))
                }

            def scroll():
                response = q.post(
                    path + "/points/scroll",
                    json={
                        "limit": 100,
                        "with_payload": True,
                        "with_vector": True,
                        "filter": owned_filter(owners[0]),
                    },
                )
                response.raise_for_status()
                return response.json()["result"]["points"]

            points = scroll()
            assert {p["id"] for p in points} == canonical
            assert all(p["vector"]["dense"] and p["vector"]["sparse"]["indices"] for p in points)
            for using, query in [("dense", [1, 0.1, 0.2]), ("sparse", sparse_vector("backups"))]:
                result = q.post(
                    path + "/points/query",
                    json={
                        "query": query,
                        "using": using,
                        "limit": 5,
                        "filter": owned_filter(owners[0]),
                    },
                )
                result.raise_for_status()
                assert result.json()["result"]["points"]
            # Seed an obsolete same-source point plus another owner's point. Neither is canonical.
            stale, unrelated = str(uuid4()), str(uuid4())
            payload = points[0]["payload"].copy()
            payload["projection_generation"] = "old"
            response = q.put(
                path + "/points?wait=true",
                json={
                    "points": [
                        {"id": stale, "vector": points[0]["vector"], "payload": payload},
                        {
                            "id": unrelated,
                            "vector": points[0]["vector"],
                            "payload": {**payload, "owner_id": owners[1]},
                        },
                    ]
                },
            )
            response.raise_for_status()
            # Exercise the external-consumer API with real Qdrant retrieval and canonical
            # hydration, without a paid embedding dependency in integration tests.
            import importlib

            monkeypatch.setattr(
                importlib.import_module("knowledge_bootstrap.search"),
                "SearchIndex",
                DeterministicEmbeddingIndex,
            )
            with TestClient(create_app(config)) as search_client:
                search_client.headers["Authorization"] = "Bearer test-owner-a-token-123"
                for mode in ("dense", "sparse", "hybrid"):
                    results = (
                        search_client.post(
                            "/v1/search", json={"query": "backups", "mode": mode, "debug": True}
                        )
                        .raise_for_status()
                        .json()["results"]
                    )
                    assert results and {r["chunk_id"] for r in results} <= canonical
                    assert all(r["source_id"] == source_id and r["uri"] for r in results)
                    assert all("score_components" in r for r in results)
                    # Orphan same-owner and cross-owner points cannot become evidence.
                    assert stale not in {r["chunk_id"] for r in results}
                    assert unrelated not in {r["chunk_id"] for r in results}
                document_id = results[0]["document_id"]
                for filters in (
                    {"source_ids": [source_id]},
                    {"document_ids": [document_id]},
                    {"formats": ["markdown"]},
                ):
                    assert (
                        search_client.post(
                            "/v1/search", json={"query": "backups", "filters": filters}
                        )
                        .raise_for_status()
                        .json()["results"]
                    )
                assert search_client.post(
                    "/v1/search", json={"query": "backups", "filters": {"formats": ["pdf"]}}
                ).raise_for_status().json() == {"results": []}
            client.post(f"/v1/sources/{source_id}/reindex")
            process_index_next(sessions, config, DeterministicEmbeddingIndex)
            assert {p["id"] for p in scroll()} == canonical
            other = q.post(
                path + "/points/scroll", json={"filter": owned_filter(owners[1]), "limit": 10}
            )
            assert [p["id"] for p in other.json()["result"]["points"]] == [unrelated]
            # A lost collection is fully regenerable from committed chunks, with the same IDs.
            q.delete(path).raise_for_status()
            jobs = rebuild_index(sessions, config, owners[0], DeterministicEmbeddingIndex)
            assert all(job.status == "ready" for job in jobs)
            assert {p["id"] for p in scroll()} == canonical
        finally:
            q.delete(path).raise_for_status()
