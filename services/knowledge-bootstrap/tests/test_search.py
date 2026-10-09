from types import SimpleNamespace
from uuid import uuid4

import httpx
import pytest
from pydantic import ValidationError

from knowledge_bootstrap.index import IndexError, SearchIndex
from knowledge_bootstrap.schemas import SearchFilters, SearchRequest
from knowledge_bootstrap.search import Candidate, diversify, shingles


@pytest.mark.parametrize("query", ["", "  \n", "a\x00b"])
def test_query_validation(query):
    with pytest.raises(ValidationError):
        SearchRequest(query=query)


@pytest.mark.parametrize(
    "payload",
    [
        {"owner_id": "other"},
        {"limit": 0},
        {"limit": 101},
        {"query": "x" * 2049},
        {"filters": {"tags": ["production"]}},
        {"filters": {"unknown": []}},
        {"filters": {"formats": ["exe"]}},
        {"filters": {"document_ids": ["invalid"]}},
        {"mode": "memory"},
    ],
)
def test_invalid_search_options(payload):
    with pytest.raises(ValidationError):
        SearchRequest.model_validate({"query": "backup", **payload})


def test_clean_query_and_reserved_empty_filters():
    assert SearchRequest(query="  Backup \n").query == "Backup"
    assert SearchFilters(tags=[]).tags == []


def candidate(text, score, *, doc=None, section="Section"):
    chunk = SimpleNamespace(
        id=uuid4(),
        document_id=doc or uuid4(),
        text=text,
        heading_path=[section],
        page_start=None,
        page_end=None,
    )
    document = SimpleNamespace(
        id=chunk.document_id, title="Handbook", uri="handbook.md", format="markdown"
    )
    source = SimpleNamespace(id=uuid4(), source_uri="handbook.md")
    return Candidate(chunk, document, source, {"relevance": score}, shingles(text), score)


def test_diversity_promotes_other_documents_and_sections_without_hard_caps():
    doc = uuid4()
    first = candidate("First evidence for a production rollout.", 0.99, doc=doc)
    repeat = candidate("Second distinct evidence for a production rollout.", 0.98, doc=doc)
    other_section = candidate(
        "A different topic in the same handbook.", 0.97, doc=doc, section="Other"
    )
    other_doc = candidate("Another document describing deployment procedures.", 0.96)
    result = diversify([repeat, other_section, other_doc, first], 4, True).results
    assert [r.chunk_id for r in result] == [
        first.chunk.id,
        other_doc.chunk.id,
        other_section.chunk.id,
        repeat.chunk.id,
    ]
    assert result[-1].score_components["diversity_multiplier"] < 1
    assert [r.score for r in result] == sorted([r.score for r in result], reverse=True)


def test_near_duplicate_evidence_removed_across_documents():
    text = " ".join(f"word{i}" for i in range(100))
    first = candidate(text, 0.99)
    duplicate = candidate(text + " trailing clarification", 0.98)
    distinct = candidate("Unrelated useful information about incident response.", 0.5)
    result = diversify([duplicate, first, distinct], 3, False).results
    assert [r.chunk_id for r in result] == [first.chunk.id, distinct.chunk.id]
    assert all(r.score_components is None for r in result)


def test_ties_are_stable_and_single_document_can_fill_limit():
    doc = uuid4()
    choices = [candidate(f"Unique passage number {i}.", 0.9, doc=doc) for i in range(4)]
    first = diversify(choices.copy(), 4, False)
    second = diversify(list(reversed(choices)), 4, False)
    assert first == second
    assert len(first.results) == 4


def test_backend_score_ties_do_not_depend_on_rebuild_insertion_order(settings):
    points = [
        {"id": "b", "score": 1.0, "payload": {}},
        {"id": "a", "score": 1.0, "payload": {}},
        {"id": "c", "score": 2.0, "payload": {}},
    ]

    def respond(_):
        return httpx.Response(200, json={"result": [{"points": points}]})

    with SearchIndex(settings, transport=httpx.MockTransport(respond)) as index:
        before = index.query({"dense": [1]}, {}, 3)
        points.reverse()
        assert index.query({"dense": [1]}, {}, 3) == before
        assert [p["id"] for p in before["dense"]] == ["c", "a", "b"]


def test_query_batch_contract_auth_and_read_only(settings):
    def respond(request):
        import json

        body = json.loads(request.content)
        assert request.method == "POST" and request.url.path.endswith("/points/query/batch")
        assert "Authorization" not in request.headers
        assert request.headers["api-key"] == "qdrant-secret"
        assert [s["using"] for s in body["searches"]] == ["dense", "sparse"]
        assert all(s["filter"] == {"must": []} and s["limit"] == 10 for s in body["searches"])
        assert all(s["with_payload"] and not s["with_vector"] for s in body["searches"])
        return httpx.Response(200, json={"result": [{"points": []}, {"points": []}]})

    config = settings.model_copy(
        update={"qdrant_api_key": __import__("pydantic").SecretStr("qdrant-secret")}
    )
    with SearchIndex(config, transport=httpx.MockTransport(respond), timeout_seconds=1) as index:
        assert index.query(
            {"dense": [1], "sparse": {"indices": [1], "values": [1]}}, {"must": []}, 10
        ) == {"dense": [], "sparse": []}
        assert index.query({}, {}, 10) == {}


@pytest.mark.parametrize(
    "result",
    [
        {},
        {"result": {}},
        {"result": []},
        {"result": [{"points": {}}]},
        {"result": [{"points": [None]}]},
        {"result": [{"points": [{"id": 3, "score": 1, "payload": {}}]}]},
        {"result": [{"points": [{"id": "id", "score": True, "payload": {}}]}]},
        {"result": [{"points": [{"id": "id", "score": 1, "payload": None}]}]},
    ],
)
def test_invalid_query_batch_is_bounded_and_sanitized(settings, result):
    with SearchIndex(
        settings, transport=httpx.MockTransport(lambda _: httpx.Response(200, json=result))
    ) as index:
        with pytest.raises(IndexError, match="invalid candidates"):
            index.query({"dense": [1]}, {}, 10)


def test_settings_reject_candidate_budget_below_result_limit(settings):
    from knowledge_bootstrap.config import Settings

    with pytest.raises(ValidationError, match="candidate limit"):
        Settings.model_validate(
            {**settings.model_dump(), "search_candidate_limit": 10, "search_max_limit": 20}
        )
