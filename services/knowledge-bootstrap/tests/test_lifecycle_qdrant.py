"""K8 handbook lifecycle with real canonical storage, projection and HTTP retrieval."""

import importlib
import json
import os
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

import httpx
import pytest
from fastapi.testclient import TestClient

from knowledge_bootstrap.app import create_app
from knowledge_bootstrap.index import IndexError, owned_filter
from knowledge_bootstrap.ingestion import process_next
from knowledge_bootstrap.pipeline import process_chunk_next, process_index_next, rebuild_index
from knowledge_bootstrap.web_crawl import CrawlResult
from knowledge_bootstrap.web_html import parse_html
from tests.test_qdrant import DeterministicEmbeddingIndex
from tests.test_web import response

pytestmark = pytest.mark.integration


def test_handbook_refresh_failure_recovery_rebuild_and_delete(
    sessions, db_settings, owners, monkeypatch, request
):
    url = os.environ.get("KNOWLEDGE_TEST_QDRANT_URL")
    if not url:
        if request.config.getoption("--require-qdrant"):
            pytest.fail("KNOWLEDGE_TEST_QDRANT_URL is required")
        pytest.skip("set KNOWLEDGE_TEST_QDRANT_URL to run real Qdrant lifecycle evaluation")
    settings = db_settings.model_copy(
        update={
            "indexing_enabled": True,
            "embedding_dimensions": 3,
            "qdrant_url": url,
            "qdrant_collection": "knowledge_lifecycle_" + uuid4().hex,
            "index_batch_size": 1,
        }
    )
    path = url + "/collections/" + settings.qdrant_collection
    root = "https://example.com/docs"

    def document(uri, heading, text):
        body = f"<main><h1>{heading}</h1><p>{text}</p></main>".encode()
        return parse_html(response(uri, body=body), settings)[0]

    policy = document(
        root, "Release handbook", "Canary deployments require oncall approval and staged rollout."
    )
    obsolete = document(
        root + "/obsolete",
        "Legacy backup policy",
        "Legacysnap snapshots use a seven day retention interval.",
    )
    crawl = CrawlResult([policy, obsolete], {"frontier_complete": True})
    monkeypatch.setattr("knowledge_bootstrap.ingestion.ingest_website", lambda *a: crawl)
    monkeypatch.setattr(
        importlib.import_module("knowledge_bootstrap.search"),
        "SearchIndex",
        DeterministicEmbeddingIndex,
    )
    report = {
        "milestone": "K8",
        "evaluated_at": datetime.now(UTC).isoformat(),
        "storage": "existing PostgreSQL test database and Qdrant; isolated owner/collection",
        "embedding": "deterministic test vectors; quality benchmark remains K6 corpus.json",
        "website": "versioned HTML fixture through real parser; crawler safety tested separately",
        "checks": [],
    }
    with httpx.Client(trust_env=False, timeout=10) as q, TestClient(create_app(settings)) as api:
        api.headers["Authorization"] = "Bearer test-owner-a-token-123"
        try:
            created = (
                api.post("/v1/sources", json={"kind": "url", "source_uri": root})
                .raise_for_status()
                .json()
            )
            source_id = created["source"]["id"]

            def run(index=DeterministicEmbeddingIndex):
                assert process_next(sessions, settings)
                assert process_chunk_next(sessions, settings)
                assert process_index_next(sessions, settings, index)

            def search(query, mode):
                return (
                    api.post(
                        "/v1/search",
                        json={
                            "query": query,
                            "mode": mode,
                            "limit": 3,
                            "filters": {"source_ids": [source_id]},
                        },
                    )
                    .raise_for_status()
                    .json()["results"]
                )

            def identities():
                docs = api.get(f"/v1/sources/{source_id}/documents").raise_for_status().json()
                chunks = [
                    c
                    for d in docs
                    for c in api.get(f"/v1/documents/{d['id']}/chunks").raise_for_status().json()
                ]
                return docs, {c["id"]: c for c in chunks}

            def points():
                return (
                    q.post(
                        path + "/points/scroll",
                        json={
                            "filter": owned_filter(owners[0]),
                            "limit": 100,
                            "with_payload": True,
                        },
                    )
                    .raise_for_status()
                    .json()["result"]["points"]
                )

            run()
            docs, chunks = identities()
            assert len(docs) == len(chunks) == 2
            for mode in ("dense", "sparse", "hybrid"):
                for query in ("oncall approval", "legacysnap retention"):
                    evidence = search(query, mode)
                    assert evidence and {r["chunk_id"] for r in evidence} <= set(chunks)
                    for row in evidence:
                        canonical = chunks[row["chunk_id"]]
                        doc = next(d for d in docs if d["id"] == row["document_id"])
                        assert row["text"] == canonical["text"]
                        assert row["uri"] == doc["uri"] and row["title"] == doc["title"]
                        assert row["heading_path"] == canonical["heading_path"]
                        assert row["source_id"] == source_id and row["format"] == "html"
            report["checks"].append(
                "initial handbook retrieval and canonical provenance in all three modes"
            )

            # Keep one page unchanged, replace the other: stable chunks coexist with new IDs.
            replacement = document(
                root + "/recovery",
                "Recovery policy",
                "Vaultsnap recovery snapshots use a thirty day retention interval.",
            )
            crawl.documents = [policy, replacement]
            job = api.post(f"/v1/sources/{source_id}/refresh").raise_for_status().json()["job"]

            class PartialFailure(DeterministicEmbeddingIndex):
                def upsert(self, points):
                    super().upsert(points)
                    raise IndexError(
                        "index_write_failed", "Simulated crash after acknowledged batch"
                    )

            run(PartialFailure)
            assert api.get("/v1/jobs/" + job["id"]).json()["error_code"] == "index_write_failed"
            updated_docs, updated = identities()
            assert len(updated_docs) == len(updated) == 2
            old_policy = next(r for r in chunks.values() if "oncall" in r["text"])
            assert old_policy["id"] in updated
            stale = set(chunks) - set(updated)
            assert stale and stale <= {p["id"] for p in points()}
            assert search("legacysnap", "sparse") == []  # Failed source is not evidence.
            retry = api.post(f"/v1/sources/{source_id}/reindex").raise_for_status().json()["job"]
            assert process_index_next(sessions, settings, DeterministicEmbeddingIndex)
            assert api.get("/v1/jobs/" + retry["id"]).json()["status"] == "ready"
            assert {p["id"] for p in points()} == set(updated)
            assert search("legacysnap", "sparse") == []
            assert search("vaultsnap", "sparse")
            report["checks"].append(
                "refresh removes absent page; partial write retains chunks; retry cleans stale data"
            )

            queries = ("oncall approval", "vaultsnap retention")
            before = {
                (mode, query): [r["chunk_id"] for r in search(query, mode)]
                for mode in ("dense", "sparse", "hybrid")
                for query in queries
            }
            q.delete(path).raise_for_status()
            rebuilt = rebuild_index(sessions, settings, owners[0], DeterministicEmbeddingIndex)
            assert len(rebuilt) == 1 and rebuilt[0].status == "ready"
            after = {
                (mode, query): [r["chunk_id"] for r in search(query, mode)]
                for mode in ("dense", "sparse", "hybrid")
                for query in queries
            }
            assert before == after
            assert {p["id"] for p in points()} == set(updated)
            report["checks"].append(
                "lost collection rebuilt from Postgres; all six query/mode rankings preserved"
            )

            assert api.delete(f"/v1/sources/{source_id}").status_code == 204
            assert api.delete(f"/v1/sources/{source_id}").status_code == 204
            assert api.get(f"/v1/sources/{source_id}").status_code == 404
            assert api.get("/v1/jobs/" + job["id"]).status_code == 404
            assert points() == []
            for mode in ("dense", "sparse", "hybrid"):
                assert search("vaultsnap", mode) == []
            report["checks"].append(
                "canonical cascade and index deletion; repeated delete succeeds; retrieval empty"
            )
            report["passed"] = True
            output = os.environ.get("KNOWLEDGE_LIFECYCLE_REPORT")
            if output:
                Path(output).write_text(json.dumps(report, indent=2) + "\n")
        finally:
            assert q.delete(path).status_code in (200, 404)
