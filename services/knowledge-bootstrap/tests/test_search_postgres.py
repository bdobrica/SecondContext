import hashlib
from uuid import UUID, uuid4

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import delete

from knowledge_bootstrap.app import create_app
from knowledge_bootstrap.index import IndexError
from knowledge_bootstrap.models import Chunk, Document, IndexConfiguration, Source
from knowledge_bootstrap.pipeline import process_index_next
from tests.test_pipeline import FakeIndex, create_and_chunk

pytestmark = pytest.mark.integration


class RetrievalIndex(FakeIndex):
    branches = None
    requests = []
    failure = None
    hook = None

    def __init__(self, settings, **kwargs):
        self.timeout = kwargs.get("timeout_seconds")

    def embed(self, texts):
        self.requests.append(("embed", texts))
        if self.failure:
            raise self.failure
        return [[1, 2, 3]]

    def query(self, vectors, filter, limit):
        self.requests.append(("query", vectors, filter, limit, self.timeout))
        if self.failure:
            raise self.failure
        if self.hook:
            type(self).hook()
        # Deliberately ignore backend filters: canonical checks must independently isolate data.
        branches = self.branches or {name: list(self.points.values()) for name in vectors}
        return {
            name: [
                {**point, "score": 1 - rank / 1000}
                for rank, point in enumerate(branches.get(name, []))
            ]
            for name in vectors
        }


@pytest.fixture
def search_config(db_settings):
    return db_settings.model_copy(
        update={
            "indexing_enabled": True,
            "embedding_dimensions": 3,
            "qdrant_collection": "search_test_" + uuid4().hex,
        }
    )


@pytest.fixture
def retrieval_client(search_config, monkeypatch, sessions):
    import importlib

    module = importlib.import_module("knowledge_bootstrap.search")
    monkeypatch.setattr(module, "SearchIndex", RetrievalIndex)
    FakeIndex.points, FakeIndex.calls, FakeIndex.writes, FakeIndex.fail_after = {}, [], 0, None
    RetrievalIndex.branches, RetrievalIndex.requests = None, []
    RetrievalIndex.failure, RetrievalIndex.hook = None, None
    with TestClient(create_app(search_config)) as client:
        client.headers["Authorization"] = "Bearer test-owner-a-token-123"
        yield client


def ingest(client, sessions, config, text="# Deployment\n\nRollback safely."):
    source_id, _ = create_and_chunk(client, sessions, config, text)
    assert process_index_next(sessions, config, FakeIndex)
    return source_id, next(
        p for p in FakeIndex.points.values() if p["payload"]["source_id"] == source_id
    )


@pytest.mark.parametrize("phase", ["before", "during"])
def test_outdated_canonical_chunks_are_not_evidence_even_if_marked_ready(
    retrieval_client, sessions, search_config, phase
):
    source_id, _ = ingest(retrieval_client, sessions, search_config)

    def outdated():
        with sessions.begin() as session:
            source = session.get(Source, UUID(source_id))
            source.metadata_json = {**source.metadata_json, "chunks_current": False}

    if phase == "before":
        outdated()
    else:
        RetrievalIndex.hook = outdated
    assert retrieval_client.post("/v1/search", json={"query": "rollback"}).json() == {"results": []}


def test_search_evidence_debug_filters_and_owner(retrieval_client, sessions, search_config, owners):
    client = retrieval_client
    source_id, point = ingest(client, sessions, search_config)
    response = client.post("/v1/search", json={"query": "Deployment rollback", "debug": True})
    assert response.status_code == 200
    result = response.json()["results"][0]
    assert result["chunk_id"] == point["id"]
    assert result["document_id"] == point["payload"]["document_id"]
    assert result["source_id"] == source_id
    assert (
        result["text"] == "# Deployment\n\nRollback safely." or "Rollback safely." in result["text"]
    )
    assert result["title"] == "Deployment" and result["heading_path"] == ["Deployment"]
    assert result["uri"] == point["payload"]["url"] and result["format"] == "markdown"
    assert result["score_components"]["dense_rank"] == 1
    assert result["score_components"]["sparse_rank"] == 1
    assert result["score_components"]["title_overlap"] == 0.5
    query = RetrievalIndex.requests[-1]
    assert query[2]["must"][:2] == [
        {"key": "kind", "match": {"value": "knowledge"}},
        {"key": "owner_id", "match": {"value": owners[0]}},
    ]
    assert query[-1] == search_config.search_timeout_seconds
    for filters in [
        {"source_ids": [source_id]},
        {"document_ids": [result["document_id"]]},
        {"formats": ["markdown"]},
        {"tags": []},
    ]:
        filtered = client.post("/v1/search", json={"query": "rollback", "filters": filters}).json()[
            "results"
        ]
        assert filtered[0]["chunk_id"] == result["chunk_id"]
        assert "score_components" not in filtered[0]
    for filters in [
        {"source_ids": [str(uuid4())]},
        {"document_ids": [str(uuid4())]},
        {"formats": ["pdf"]},
    ]:
        assert client.post("/v1/search", json={"query": "rollback", "filters": filters}).json() == {
            "results": []
        }
    assert client.post(
        "/v1/search",
        json={"query": "rollback"},
        headers={"Authorization": "Bearer test-owner-b-token-456"},
    ).json() == {"results": []}
    client.headers.pop("Authorization")
    assert client.post("/v1/search", json={"query": "rollback"}).status_code == 401


@pytest.mark.parametrize("mode", ["dense", "sparse", "hybrid"])
def test_retrieval_modes_and_disabled_indexing(retrieval_client, sessions, search_config, mode):
    ingest(retrieval_client, sessions, search_config)
    search_config.indexing_enabled = False
    response = retrieval_client.post("/v1/search", json={"query": "rollback", "mode": mode})
    assert response.status_code == 200 and response.json()["results"]
    vectors = RetrievalIndex.requests[-1][1]
    assert set(vectors) == ({"dense", "sparse"} if mode == "hybrid" else {mode})
    assert any(r[0] == "embed" for r in RetrievalIndex.requests) == (mode != "sparse")


def test_hybrid_promotes_shared_evidence_over_single_mode_distractors(
    retrieval_client, sessions, search_config
):
    _, shared = ingest(
        retrieval_client, sessions, search_config, "# Reference A\nEvidence about the procedure."
    )
    _, dense_only = ingest(
        retrieval_client,
        sessions,
        search_config,
        "# Reference B\nA semantically related distractor.",
    )
    _, sparse_only = ingest(
        retrieval_client, sessions, search_config, "# Reference C\nA lexical false positive."
    )
    RetrievalIndex.branches = {"dense": [dense_only, shared], "sparse": [sparse_only, shared]}
    winners = {
        mode: retrieval_client.post(
            "/v1/search", json={"query": "procedure", "mode": mode, "limit": 1}
        ).json()["results"][0]["chunk_id"]
        for mode in ["dense", "sparse", "hybrid"]
    }
    assert winners == {
        "dense": dense_only["id"],
        "sparse": sparse_only["id"],
        "hybrid": shared["id"],
    }


@pytest.mark.parametrize(
    "bad",
    [
        "hash",
        "generation",
        "owner",
        "source",
        "document",
        "chunk",
        "kind",
        "orphan",
        "missing",
        "malformed",
    ],
)
def test_stale_or_untrusted_points_cannot_supply_evidence(
    retrieval_client, sessions, search_config, bad
):
    _, point = ingest(retrieval_client, sessions, search_config)
    bad_point = {**point, "payload": point["payload"].copy()}
    key = {
        "hash": "content_hash",
        "generation": "projection_generation",
        "owner": "owner_id",
        "source": "source_id",
        "document": "document_id",
        "chunk": "chunk_id",
        "kind": "kind",
    }.get(bad)
    if key:
        bad_point["payload"][key] = "wrong"
    elif bad == "orphan":
        bad_point["id"] = str(uuid4())
    elif bad == "malformed":
        bad_point["id"] = "not-a-uuid"
    else:
        bad_point["payload"] = {}
    RetrievalIndex.branches = {"dense": [bad_point], "sparse": [bad_point]}
    assert retrieval_client.post("/v1/search", json={"query": "rollback"}).json() == {"results": []}


def test_canonical_text_and_provenance_override_payload(retrieval_client, sessions, search_config):
    _, point = ingest(retrieval_client, sessions, search_config)
    point["payload"].update(
        {
            "title": "FORGED",
            "url": "FORGED",
            "text": "FORGED",
            "heading_path": ["FORGED"],
            "format": "pdf",
        }
    )
    result = retrieval_client.post("/v1/search", json={"query": "rollback"}).json()["results"][0]
    assert "FORGED" not in str(result) and result["format"] == "markdown"
    with sessions() as session:
        canonical = session.get(Chunk, UUID(result["chunk_id"]))
        assert canonical.text == result["text"]
        doc = session.get(Document, canonical.document_id)
        assert doc.uri == result["uri"] and doc.title == result["title"]


def test_source_refresh_during_backend_read_hides_old_projection(
    retrieval_client, sessions, search_config
):
    source_id, _ = ingest(retrieval_client, sessions, search_config)

    def change_state():
        with sessions.begin() as session:
            session.get(Source, UUID(source_id)).status = "indexing"

    RetrievalIndex.hook = change_state
    assert retrieval_client.post("/v1/search", json={"query": "rollback"}).json() == {"results": []}


@pytest.mark.parametrize("change", ["model", "manifest", "source_recipe"])
def test_recipe_mismatch_does_not_search_or_write(
    retrieval_client, sessions, search_config, change
):
    source_id, _ = ingest(retrieval_client, sessions, search_config)
    if change == "model":
        search_config.embedding_model = "different-model"
    elif change == "manifest":
        with sessions.begin() as session:
            session.execute(
                delete(IndexConfiguration).where(
                    IndexConfiguration.target
                    == hashlib.sha256(
                        (search_config.qdrant_url + "/" + search_config.qdrant_collection).encode()
                    ).hexdigest()
                )
            )
    else:
        with sessions.begin() as session:
            source = session.get(Source, UUID(source_id))
            source.metadata_json = {
                **source.metadata_json,
                "index": {**source.metadata_json["index"], "recipe": {}},
            }
    response = retrieval_client.post("/v1/search", json={"query": "rollback"})
    if change == "source_recipe":
        assert response.json() == {"results": []}
    else:
        assert response.status_code == 503 and not RetrievalIndex.requests
    assert FakeIndex.writes == 1  # No retrieval-side mutations.


@pytest.mark.parametrize(
    "code,status_code",
    [
        ("index_timeout", "search_timeout"),
        ("embedding_failed", "search_unavailable"),
        ("search_invalid", "search_unavailable"),
    ],
)
def test_backend_failure_is_sanitized_and_explicit(
    retrieval_client, sessions, search_config, code, status_code
):
    ingest(retrieval_client, sessions, search_config)
    RetrievalIndex.failure = IndexError(code, "SECRET backend response with credentials")
    response = retrieval_client.post("/v1/search", json={"query": "rollback"})
    assert response.status_code == 503 and response.json()["error"]["code"] == status_code
    assert "SECRET" not in response.text


def test_server_limit_query_token_bound_empty_tenant_and_punctuation(
    retrieval_client, sessions, search_config
):
    client = retrieval_client
    assert client.post("/v1/search", json={"query": "rollback"}).json() == {"results": []}
    assert not RetrievalIndex.requests
    assert client.post("/v1/search", json={"query": "rollback", "limit": 21}).status_code == 422
    assert client.post("/v1/search", json={"query": "😀" * 1024}).status_code == 422
    ingest(client, sessions, search_config)
    assert client.post("/v1/search", json={"query": "!!!", "mode": "sparse"}).json() == {
        "results": []
    }
    assert not any(r[0] == "embed" for r in RetrievalIndex.requests)


def test_exact_status_code_survives_generic_retry_title_match(
    retrieval_client, sessions, search_config
):
    _, generic = ingest(
        retrieval_client,
        sessions,
        search_config,
        "# Retry policy defaults\nUse 3 retries and exponential backoff.",
    )
    _, exact = ingest(
        retrieval_client,
        sessions,
        search_config,
        "# HTTP rate limiting\nA 429 response signals a quota exceeded. Respect Retry-After.",
    )
    RetrievalIndex.branches = {"dense": [generic, exact], "sparse": [generic, exact]}
    for mode in ("dense", "sparse", "hybrid"):
        result = retrieval_client.post(
            "/v1/search", json={"query": "429 Retry-After", "mode": mode, "limit": 1, "debug": True}
        ).json()["results"][0]
        assert result["chunk_id"] == exact["id"]
        assert result["score_components"]["identifier_overlap"] == 1


def test_both_ready_tenants_remain_isolated_if_backend_ignores_owner_filter(
    retrieval_client, sessions, search_config
):
    _, a = ingest(
        retrieval_client, sessions, search_config, "# Tenant A\nPrivate deployment rules for A."
    )
    retrieval_client.headers["Authorization"] = "Bearer test-owner-b-token-456"
    _, b = ingest(
        retrieval_client, sessions, search_config, "# Tenant B\nPrivate deployment rules for B."
    )
    for token, expected in [("test-owner-a-token-123", a), ("test-owner-b-token-456", b)]:
        retrieval_client.headers["Authorization"] = "Bearer " + token
        result = retrieval_client.post("/v1/search", json={"query": "deployment rules"}).json()[
            "results"
        ]
        assert [r["chunk_id"] for r in result] == [expected["id"]]
