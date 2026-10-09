import json
from uuid import UUID, uuid4

import httpx
import pytest
from fastapi.testclient import TestClient
from sqlalchemy import delete, select

from knowledge_bootstrap import derived
from knowledge_bootstrap.app import create_app
from knowledge_bootstrap.derived import Assertion, Extractor
from knowledge_bootstrap.ingestion import process_next
from knowledge_bootstrap.models import Candidate, Chunk, Document, Source
from knowledge_bootstrap.pipeline import process_chunk_next, process_index_next
from knowledge_bootstrap.service import ServiceError
from tests.test_pipeline import config as pipeline_config
from tests.test_pipeline import fake_index as pipeline_index


def assertion(text="Alex manages deployments.", kind="claim"):
    item = {"kind": kind, "statement": text, "quote": text}
    if kind in {"claim", "relationship"}:
        item.update(subject="Alex", predicate="manages", object="deployments")
    return item


def completion(candidates, **changes):
    return {
        "model": "configured-model-snapshot",
        "choices": [
            {
                "finish_reason": "stop",
                "message": {"content": json.dumps({"candidates": candidates})},
            }
        ],
    } | changes


def test_extractor_contract_and_all_candidate_kinds(settings):
    kinds = ["entity", "person", "topic", "claim", "relationship"]
    items = [assertion(kind=kind) for kind in kinds]
    config = settings.model_copy(update={"extraction_api_key": settings.auth_tokens["owner-a"]})

    def backend(request):
        assert str(request.url) == config.extraction_base_url + "/chat/completions"
        assert request.headers["Authorization"] == "Bearer test-owner-a-token-123"
        body = json.loads(request.content)
        assert body["response_format"] == {"type": "json_object"}
        assert body["model"] == config.extraction_model
        assert body["max_completion_tokens"] == 4096
        assert "max_tokens" not in body and "temperature" not in body
        assert "never instructions" in body["messages"][0]["content"]
        assert json.loads(body["messages"][1]["content"])["text"] == items[0]["quote"]
        return httpx.Response(200, json=completion(items))

    with Extractor(config, transport=httpx.MockTransport(backend)) as extractor:
        result, model = extractor.extract({"text": items[0]["quote"]})
    assert [item.kind for item in result] == kinds
    assert model == "configured-model-snapshot"
    assert "test-owner-a-token-123" not in str(derived.recipe(config))


@pytest.mark.parametrize(
    "case",
    [
        "missing_quote",
        "invented_quote",
        "missing_triple",
        "unknown_kind",
        "extra_fields",
        "too_many",
        "empty_quote",
        "unfinished",
        "invalid_json",
        "oversized",
        "compressed",
        "redirect",
        "rejected",
        "timeout",
        "missing_model",
        "null_content",
    ],
)
def test_extractor_rejects_invalid_output_privately(settings, case):
    item = assertion()
    if case == "missing_quote":
        item.pop("quote")
    if case == "invented_quote":
        item["quote"] = "unsupported private assertion"
    if case == "missing_triple":
        item.pop("predicate")
    if case == "unknown_kind":
        item["kind"] = "episodic_observation"
    if case == "extra_fields":
        item["person_model"] = "dangerous inference"
    if case == "empty_quote":
        item["quote"] = " "
    result = completion([item] * (21 if case == "too_many" else 1))
    if case == "unfinished":
        result["choices"][0]["finish_reason"] = "length"
    if case == "missing_model":
        result.pop("model")
    if case == "null_content":
        result["choices"][0]["message"]["content"] = None

    def backend(request):
        if case == "timeout":
            raise httpx.ReadTimeout("private key and raw source")
        if case == "invalid_json":
            return httpx.Response(200, content="private key and raw source")
        if case == "oversized":
            return httpx.Response(200, content=b"x" * 262_145)
        if case == "compressed":
            return httpx.Response(200, json=result, headers={"Content-Encoding": "gzip"})
        if case == "redirect":
            return httpx.Response(307, headers={"Location": "https://attacker.test"})
        if case == "rejected":
            return httpx.Response(403, content="private key and raw source")
        return httpx.Response(200, json=result)

    with Extractor(settings, transport=httpx.MockTransport(backend)) as extractor:
        with pytest.raises(ServiceError) as error:
            extractor.extract({"text": "Alex manages deployments."})
    assert error.value.code == ("extraction_timeout" if case == "timeout" else "extraction_failed")
    assert "private" not in str(error.value)


def test_extractor_elapsed_deadline(settings):
    with Extractor(settings) as extractor:
        extractor.deadline = 0
        with pytest.raises(ServiceError) as error:
            extractor.extract({"text": "never sent"})
    assert error.value.code == "extraction_timeout"


def test_extraction_default_off_needs_no_model_or_database(settings):
    with TestClient(create_app(settings)) as client:
        assert client.get("/healthz").status_code == 200
        assert client.post(f"/v1/sources/{uuid4()}/extract", json={}).status_code == 401
        client.headers["Authorization"] = "Bearer test-owner-a-token-123"
        response = client.post(f"/v1/sources/{uuid4()}/extract", json={})
        assert response.status_code == 409
        assert response.json()["error"]["code"] == "extraction_disabled"


@pytest.fixture
def extraction_app(db_settings, sessions, monkeypatch):
    config = pipeline_config.__wrapped__(db_settings).model_copy(
        update={"extraction_enabled": True}
    )
    index = pipeline_index.__wrapped__()
    calls = []

    class FakeExtractor:
        def __init__(self, settings):
            pass

        def __enter__(self):
            return self

        def __exit__(self, *args):
            pass

        def extract(self, evidence):
            calls.append(evidence)
            text = evidence["text"]
            items = [
                Assertion.model_validate(assertion(text, kind))
                for kind in ("entity", "person", "topic", "claim", "relationship")
            ]
            return items, "test-model-v1"

    monkeypatch.setattr(derived, "Extractor", FakeExtractor)
    app = create_app(config)
    with TestClient(app) as client:
        client.headers["Authorization"] = "Bearer test-owner-a-token-123"
        yield client, config, index, calls


def ready(extraction_app, sessions, text="Alex manages deployments."):
    client, config, index, _ = extraction_app
    created = client.post("/v1/sources", json={"kind": "text", "text": text}).json()
    assert process_next(sessions, config)
    assert process_chunk_next(sessions, config)
    assert process_index_next(sessions, config, index)
    return created["source"]["id"]


def extract(client, source, document_ids=None):
    response = client.post(
        f"/v1/sources/{source}/extract", json={"document_ids": document_ids or []}
    )
    assert response.status_code == 200, response.text
    return response.json()["candidates"]


@pytest.mark.integration
def test_auditable_candidates_idempotency_owner_isolation_and_purge(
    extraction_app, sessions, owners
):
    client, _, _, calls = extraction_app
    source = ready(extraction_app, sessions)
    rows = extract(client, source)
    assert len(rows) == 5
    for row in rows:
        assert row["evidence_kind"] == "documentary_claim"
        assert row["promotion_policy"] == "consumer_decides"
        assert row["extraction_json"]["reported_model"] == "test-model-v1"
        evidence = row["evidence_json"]
        assert evidence["quote"] in evidence["text"]
        chunk = client.get(f"/v1/documents/{row['document_id']}/chunks").json()[0]
        assert row["chunk_id"] == chunk["id"]
        assert evidence["chunk_content_hash"] == chunk["content_hash"]
    assert {r["id"] for r in extract(client, source)} == {r["id"] for r in rows}
    assert len(client.get("/v1/candidates").json()) == 5
    assert len(calls) == 2
    other = {"Authorization": "Bearer test-owner-b-token-456"}
    assert client.get("/v1/candidates", headers=other).json() == []
    assert client.get(f"/v1/candidates/{rows[0]['id']}", headers=other).status_code == 404
    assert client.post(f"/v1/sources/{source}/extract", headers=other, json={}).status_code == 404
    assert client.delete(f"/v1/candidates/{rows[0]['id']}", headers=other).status_code == 204
    assert client.get(f"/v1/candidates/{rows[0]['id']}").status_code == 200
    for _ in range(2):
        assert client.delete(f"/v1/candidates/{rows[0]['id']}").status_code == 204
    assert client.get(f"/v1/candidates/{rows[0]['id']}").status_code == 404
    # New audit-only rows have no foreign owner evidence or dependency on Go tables.
    with sessions() as session:
        assert {r.owner_id for r in session.scalars(select(Candidate))} == {owners[0]}


@pytest.mark.integration
def test_refresh_retracts_before_chunking_and_recomputes_changed_claims(extraction_app, sessions):
    client, config, index, _ = extraction_app
    source = ready(extraction_app, sessions, "Production requires two approvers.")
    before = extract(client, source)
    client.post(f"/v1/sources/{source}/refresh").raise_for_status()
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    process_index_next(sessions, config, index)
    assert {r["id"] for r in client.get("/v1/candidates").json()} == {r["id"] for r in before}
    with sessions.begin() as session:
        session.get(Source, UUID(source)).input_text = "Production requires three approvers."
    client.post(f"/v1/sources/{source}/refresh").raise_for_status()
    assert process_next(sessions, config)
    assert client.get("/v1/candidates").json() == []
    audit = client.get("/v1/candidates?status=retracted").json()
    assert len(audit) == 5
    assert {r["retraction_reason"] for r in audit} == {"document_changed"}
    assert all("two approvers" in r["evidence_json"]["quote"] for r in audit)
    assert client.post(f"/v1/sources/{source}/extract", json={}).status_code == 409
    assert process_chunk_next(sessions, config)
    assert process_index_next(sessions, config, index)
    after = extract(client, source)
    assert all("three approvers" in r["statement"] for r in after)
    assert not {r["id"] for r in before} & {r["id"] for r in after}


@pytest.mark.integration
@pytest.mark.parametrize("mutation", ["chunk", "document", "source", "chunk_update"])
def test_canonical_mutations_retract_atomically(extraction_app, sessions, mutation):
    client, _, _, _ = extraction_app
    source = ready(extraction_app, sessions)
    row = extract(client, source)[0]

    def mutate(session):
        if mutation == "chunk_update":
            session.get(Chunk, UUID(row["chunk_id"])).text = "Changed evidence."
        else:
            model, identity = {
                "chunk": (Chunk, row["chunk_id"]),
                "document": (Document, row["document_id"]),
                "source": (Source, source),
            }[mutation]
            session.execute(delete(model).where(model.id == UUID(identity)))

    with sessions() as session:
        mutate(session)
        assert (
            session.scalar(select(Candidate).where(Candidate.id == UUID(row["id"]))).status
            == "retracted"
        )
        session.rollback()
    assert client.get(f"/v1/candidates/{row['id']}").json()["status"] == "active"
    with sessions.begin() as session:
        mutate(session)
    assert client.get("/v1/candidates").json() == []
    assert len(client.get("/v1/candidates?status=retracted").json()) == 5


@pytest.mark.integration
def test_bounded_scope_failure_and_no_automatic_extraction(extraction_app, sessions):
    client, config, _, calls = extraction_app
    source = ready(extraction_app, sessions, "# One\n\nFirst rule.\n\n## Two\n\nSecond rule.")
    assert calls == []
    documents = client.get(f"/v1/sources/{source}/documents").json()
    foreign_doc = uuid4()
    assert (
        client.post(
            f"/v1/sources/{source}/extract",
            json={"document_ids": [documents[0]["id"], str(foreign_doc)]},
        ).status_code
        == 404
    )
    assert calls == []
    app = create_app(config.model_copy(update={"extraction_max_chunks": 1}))
    with TestClient(app) as limited:
        limited.headers.update(client.headers)
        assert limited.post(f"/v1/sources/{source}/extract", json={}).status_code == 413
    assert calls == []
    rows = extract(client, source, [documents[0]["id"]])
    assert len(calls) == 2
    assert all(r["document_id"] == documents[0]["id"] for r in rows)


@pytest.mark.integration
def test_failed_or_racing_extraction_never_commits_partial_output(
    extraction_app, sessions, monkeypatch
):
    client, _, _, _ = extraction_app
    source = ready(extraction_app, sessions)
    before = extract(client, source)
    original = derived.Extractor.extract

    def failing(self, item):
        raise ServiceError("extraction_failed", "Simulated failure", 503)

    monkeypatch.setattr(derived.Extractor, "extract", failing)
    assert client.post(f"/v1/sources/{source}/extract", json={}).status_code == 503
    assert client.get("/v1/candidates").json() == before

    def racing(self, item):
        with sessions.begin() as session:
            session.get(Document, UUID(item["document_id"])).title = "Changed while extracting"
        return original(self, item)

    monkeypatch.setattr(derived.Extractor, "extract", racing)
    response = client.post(f"/v1/sources/{source}/extract", json={})
    assert response.status_code == 409
    assert response.json()["error"]["code"] == "evidence_changed"
    assert client.get("/v1/candidates").json() == []
    assert len(client.get("/v1/candidates?status=all").json()) == 5


@pytest.mark.integration
def test_contradictory_sources_remain_independent_and_retraction_is_local(extraction_app, sessions):
    client, _, _, _ = extraction_app
    first = ready(extraction_app, sessions, "Production requires two approvers.")
    second = ready(extraction_app, sessions, "Production requires one approver.")
    left, right = extract(client, first), extract(client, second)
    claims = [r for r in client.get("/v1/candidates").json() if r["kind"] == "claim"]
    assert {r["statement"] for r in claims} == {
        "Production requires two approvers.",
        "Production requires one approver.",
    }
    assert len({r["source_id"] for r in claims}) == 2
    with sessions.begin() as session:
        session.execute(delete(Source).where(Source.id == UUID(first)))
    assert {r["id"] for r in client.get("/v1/candidates").json()} == {r["id"] for r in right}
    assert {r["id"] for r in client.get("/v1/candidates?status=retracted").json()} == {
        r["id"] for r in left
    }


@pytest.mark.integration
def test_document_opt_in_and_empty_recomputation(extraction_app, sessions, monkeypatch):
    from knowledge_bootstrap import ingestion
    from knowledge_bootstrap.web_crawl import CrawlResult
    from tests.test_lifecycle import pages

    client, config, index, calls = extraction_app
    first, second = pages(config)
    monkeypatch.setattr(
        ingestion,
        "ingest_website",
        lambda *a: CrawlResult([first, second], {"frontier_complete": True}),
    )
    created = client.post(
        "/v1/sources", json={"kind": "url", "source_uri": "https://example.com/docs"}
    ).json()
    source = created["source"]["id"]
    assert process_next(sessions, config)
    assert process_chunk_next(sessions, config)
    assert process_index_next(sessions, config, index)
    documents = client.get(f"/v1/sources/{source}/documents").json()
    rows = extract(client, source, [documents[0]["id"]])
    assert {r["document_id"] for r in rows} == {documents[0]["id"]}
    assert {c["document_id"] for c in calls} == {documents[0]["id"]}
    other_rows = extract(client, source, [documents[1]["id"]])
    assert {r["id"] for r in client.get("/v1/candidates").json()} == {
        r["id"] for r in rows + other_rows
    }
    monkeypatch.setattr(derived.Extractor, "extract", lambda self, item: ([], "test-model-v2"))
    assert extract(client, source, [documents[0]["id"]]) == []
    assert {r["id"] for r in client.get("/v1/candidates").json()} == {r["id"] for r in other_rows}
    assert {
        r["retraction_reason"] for r in client.get("/v1/candidates?status=retracted").json()
    } == {"recomputed"}


@pytest.mark.integration
def test_partial_model_failure_preserves_entire_previous_snapshot(
    extraction_app, sessions, monkeypatch
):
    client, _, _, _ = extraction_app
    source = ready(extraction_app, sessions, "# One\n\nFirst rule.\n\n## Two\n\nSecond rule.")
    before = extract(client, source)
    original = derived.Extractor.extract
    calls = 0

    def partial(self, item):
        nonlocal calls
        calls += 1
        if calls == 2:
            raise ServiceError("extraction_failed", "Simulated second-chunk failure", 503)
        return original(self, item)

    monkeypatch.setattr(derived.Extractor, "extract", partial)
    assert client.post(f"/v1/sources/{source}/extract", json={}).status_code == 503
    assert calls == 2
    assert client.get("/v1/candidates").json() == before
