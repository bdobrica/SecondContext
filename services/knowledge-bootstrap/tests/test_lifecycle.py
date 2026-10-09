"""Lifecycle regressions against canonical Postgres; projections use the K5 fault harness."""

from dataclasses import replace
from pathlib import Path
from uuid import UUID

import pytest
from sqlalchemy import select
from sqlalchemy.orm import Session

from knowledge_bootstrap import ingestion, pipeline
from knowledge_bootstrap.ingestion import process_next
from knowledge_bootstrap.models import Chunk, Source
from knowledge_bootstrap.pipeline import process_chunk_next, process_index_next
from knowledge_bootstrap.web_crawl import CrawlResult
from knowledge_bootstrap.web_html import parse_html
from tests.test_pipeline import config as pipeline_config
from tests.test_pipeline import fake_index as pipeline_index
from tests.test_pipeline import snapshot
from tests.test_web import response

pytestmark = pytest.mark.integration


@pytest.fixture
def config(db_settings):
    return pipeline_config.__wrapped__(db_settings)


@pytest.fixture
def fake_index():
    return pipeline_index.__wrapped__()


@pytest.mark.parametrize("format", ["auto", "pdf", "docx"])
def test_unchanged_refresh_skips_parsing_and_chunking(
    client,
    sessions,
    config,
    fake_index,
    monkeypatch,
    format,  # noqa: F811
):
    if format == "auto":
        created = client.post(
            "/v1/sources", json={"kind": "text", "text": "# Handbook\n\nKeep immutable copies."}
        ).json()
    else:
        path = Path(__file__).parent / "fixtures" / "binary" / ("handbook." + format)
        created = client.post(
            "/v1/sources/upload", files={"file": (path.name, path.read_bytes())}
        ).json()
    source_id = created["source"]["id"]
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    process_index_next(sessions, config, fake_index)
    before = snapshot(sessions, source_id)
    with sessions() as s:
        timestamps = {c.id: c.created_at for c in s.scalars(select(Chunk))}
    monkeypatch.setattr(ingestion, "parse_text", lambda *a: pytest.fail("reparsed text"))
    monkeypatch.setattr(ingestion, "parse_binary", lambda *a: pytest.fail("reparsed binary"))
    monkeypatch.setattr(pipeline, "chunk_blocks", lambda *a: pytest.fail("rechunked"))
    client.post(f"/v1/sources/{source_id}/refresh").raise_for_status()
    assert process_next(sessions, config)
    assert process_chunk_next(sessions, config)
    # Replaying projection still repairs externally lost points on an unchanged refresh.
    fake_index.points.clear()
    assert process_index_next(sessions, config, fake_index)
    assert snapshot(sessions, source_id) == before
    assert set(fake_index.points) == {row[0] for row in before}
    with sessions() as s:
        assert {c.id: c.created_at for c in s.scalars(select(Chunk))} == timestamps


def test_changed_parser_limits_do_not_bypass_validation(
    client,
    sessions,
    config,
    fake_index,  # noqa: F811
):
    created = client.post(
        "/v1/sources", json={"kind": "text", "format": "json", "text": '{"a":{"b":true}}'}
    ).json()
    source_id = created["source"]["id"]
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    process_index_next(sessions, config, fake_index)
    before = snapshot(sessions, source_id)
    job = client.post(f"/v1/sources/{source_id}/refresh").json()["job"]
    process_next(sessions, config.model_copy(update={"max_parse_depth": 1}))
    failed = client.get("/v1/jobs/" + job["id"]).json()
    assert failed["error_code"] == "nesting_too_deep"
    assert snapshot(sessions, source_id) == before


def test_failed_chunking_cannot_reindex_stale_chunks_under_new_documents(
    client, sessions, config, fake_index
):
    created = client.post(
        "/v1/sources", json={"kind": "text", "text": "# Policy\n\nOriginal rule."}
    ).json()
    source_id = created["source"]["id"]
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    process_index_next(sessions, config, fake_index)
    before = snapshot(sessions, source_id)
    changed = "# Policy\n\nChanged rule.\n\n## Backup\nKeep copies."
    with sessions.begin() as s:
        s.get(Source, UUID(source_id)).input_text = changed
    job = client.post(f"/v1/sources/{source_id}/refresh").json()["job"]
    process_next(sessions, config)
    process_chunk_next(sessions, config.model_copy(update={"max_chunks_per_source": 1}))
    assert client.get("/v1/jobs/" + job["id"]).json()["error_code"] == "chunk_limit_exceeded"
    assert snapshot(sessions, source_id) == before
    response = client.post(f"/v1/sources/{source_id}/reindex")
    assert response.status_code == 409
    assert response.json()["error"]["code"] == "canonical_chunks_outdated"
    # A later parsing failure does not make the obsolete snapshot reindexable either.
    with sessions.begin() as s:
        s.get(Source, UUID(source_id)).input_text = "{invalid"
        s.get(Source, UUID(source_id)).format = "json"
    client.post(f"/v1/sources/{source_id}/refresh")
    process_next(sessions, config)
    assert client.post(f"/v1/sources/{source_id}/reindex").status_code == 409
    with sessions.begin() as s:
        s.get(Source, UUID(source_id)).input_text = changed
        s.get(Source, UUID(source_id)).format = "markdown"
    client.post(f"/v1/sources/{source_id}/refresh")
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    process_index_next(sessions, config, fake_index)
    assert snapshot(sessions, source_id) != before
    assert client.post(f"/v1/sources/{source_id}/reindex").status_code == 202


def pages(settings):
    first, _ = parse_html(response(), settings)
    second, _ = parse_html(
        replace(
            response(),
            requested_url="https://example.com/docs/setup",
            final_url="https://example.com/docs/setup",
        ),
        settings,
    )
    return first, second


@pytest.mark.parametrize(
    "metadata,remaining",
    [
        ({"frontier_complete": True}, 1),
        ({"frontier_complete": False, "page_limit_reached": True}, 2),
        ({"frontier_complete": True, "frontier_truncated": True}, 2),
        ({"frontier_complete": True, "skipped_pages": [{"error_code": "web_timeout"}]}, 2),
        ({}, 2),
        ({"frontier_complete": False, "missing_pages": ["https://example.com/docs/setup"]}, 1),
        ({"frontier_complete": False, "missing_pages": ["https://example.com/docs"]}, 2),
    ],
)
def test_website_refresh_absence_policy_and_stale_point_cleanup(
    client,
    sessions,
    config,
    fake_index,
    monkeypatch,
    metadata,
    remaining,  # noqa: F811
):
    first, second = pages(config)
    result = CrawlResult([first, second], {"frontier_complete": True})
    monkeypatch.setattr(ingestion, "ingest_website", lambda *a: result)
    created = client.post(
        "/v1/sources", json={"kind": "url", "source_uri": "https://example.com/docs"}
    ).json()
    source_id = created["source"]["id"]
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    process_index_next(sessions, config, fake_index)
    before = snapshot(sessions, source_id)
    original_docs = client.get(f"/v1/sources/{source_id}/documents").json()
    result.documents, result.metadata = [first], metadata
    client.post(f"/v1/sources/{source_id}/refresh")
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    process_index_next(sessions, config, fake_index)
    current = client.get(f"/v1/sources/{source_id}/documents").json()
    assert len(current) == remaining
    assert {d["id"] for d in current} <= {d["id"] for d in original_docs}
    chunks = snapshot(sessions, source_id)
    assert set(fake_index.points) == {row[0] for row in chunks}
    assert {row[0] for row in chunks} <= {row[0] for row in before}
    if remaining == 1:
        assert current[0]["uri"] == first.extra_metadata["final_url"]
    source = client.get(f"/v1/sources/{source_id}").json()
    assert source["metadata_json"]["crawl"]["canonical_documents"] == remaining


def test_website_removal_transaction_rollback_and_retry(
    client,
    sessions,
    config,
    fake_index,
    monkeypatch,  # noqa: F811
):
    first, second = pages(config)
    result = CrawlResult([first, second], {"frontier_complete": True})
    monkeypatch.setattr(ingestion, "ingest_website", lambda *a: result)
    created = client.post(
        "/v1/sources", json={"kind": "url", "source_uri": "https://example.com/docs"}
    ).json()
    source_id = created["source"]["id"]
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    process_index_next(sessions, config, fake_index)
    before = snapshot(sessions, source_id)
    result.documents = [first]
    client.post(f"/v1/sources/{source_id}/refresh")
    original = Session.flush

    def interrupted(session, *args, **kwargs):
        if any(isinstance(row, Source) and row.status == "chunking" for row in session.dirty):
            raise RuntimeError("interrupted refresh")
        return original(session, *args, **kwargs)

    with monkeypatch.context() as patch:
        patch.setattr(Session, "flush", interrupted)
        with pytest.raises(RuntimeError):
            process_next(sessions, config)
    assert snapshot(sessions, source_id) == before
    assert len(client.get(f"/v1/sources/{source_id}/documents").json()) == 2
    assert process_next(sessions, config)
    assert len(client.get(f"/v1/sources/{source_id}/documents").json()) == 1


def test_repeated_partial_crawls_cannot_grow_retained_canonical_pages_unbounded(
    client, sessions, config, fake_index, monkeypatch
):
    limited = config.model_copy(update={"web_max_pages": 2})
    first, second = pages(limited)
    result = CrawlResult([first, second], {"frontier_complete": True})
    monkeypatch.setattr(ingestion, "ingest_website", lambda *a: result)
    created = client.post(
        "/v1/sources", json={"kind": "url", "source_uri": "https://example.com/docs"}
    ).json()
    source_id = created["source"]["id"]
    process_next(sessions, limited)
    process_chunk_next(sessions, limited)
    process_index_next(sessions, limited, fake_index)
    before = snapshot(sessions, source_id)
    original = client.get(f"/v1/sources/{source_id}").json()["content_hash"]
    third, _ = parse_html(response("https://example.com/docs/third"), limited)
    result.documents = [first, third]
    result.metadata = {"frontier_complete": False, "page_limit_reached": True}
    job = client.post(f"/v1/sources/{source_id}/refresh").json()["job"]
    process_next(sessions, limited)
    assert client.get("/v1/jobs/" + job["id"]).json()["error_code"] == "crawl_limit_exceeded"
    assert len(client.get(f"/v1/sources/{source_id}/documents").json()) == 2
    assert client.get(f"/v1/sources/{source_id}").json()["content_hash"] == original
    assert snapshot(sessions, source_id) == before
    # A complete crawl safely replaces the missing old page within the same bound.
    result.metadata = {"frontier_complete": True}
    client.post(f"/v1/sources/{source_id}/refresh")
    process_next(sessions, limited)
    process_chunk_next(sessions, limited)
    process_index_next(sessions, limited, fake_index)
    docs = client.get(f"/v1/sources/{source_id}/documents").json()
    assert {d["uri"] for d in docs} == {
        first.extra_metadata["final_url"],
        third.extra_metadata["final_url"],
    }


def test_metrics_are_authenticated_owner_scoped_and_count_search_failures(
    client,
    sessions,
    config,
    fake_index,
    monkeypatch,  # noqa: F811
):
    import importlib

    app_module = importlib.import_module("knowledge_bootstrap.app")
    first = client.post("/v1/sources", json={"kind": "text", "text": "Handbook"}).json()
    process_next(sessions, config)
    process_chunk_next(sessions, config)
    process_index_next(sessions, config, fake_index)
    client.post("/v1/sources", json={"kind": "text", "text": "{broken", "format": "json"})
    process_next(sessions, config)
    monkeypatch.setattr(app_module, "search", lambda *a: {"results": []})
    client.post("/v1/search", json={"query": "private query", "mode": "sparse"}).raise_for_status()
    from knowledge_bootstrap.service import ServiceError

    def fail(*a):
        raise ServiceError("search_unavailable", "Unavailable", 503)

    monkeypatch.setattr(app_module, "search", fail)
    assert client.post("/v1/search", json={"query": "secret", "mode": "sparse"}).status_code == 503
    metrics = client.get("/v1/metrics").raise_for_status().json()
    assert metrics["canonical"] == {"sources": 2, "documents": 1, "chunks": 1}
    assert metrics["ingestion"]["jobs_by_status"] == {"ready": 1, "failed": 1}
    assert metrics["ingestion"]["failures_by_stage"] == {"parsing": 1}
    assert metrics["ingestion"]["latency"]["completed_attempts"] == 2
    assert metrics["ingestion"]["latency"]["seconds_sum"] >= 0
    assert metrics["search"]["modes"]["sparse"]["requests"] == 2
    assert metrics["search"]["modes"]["sparse"]["failures"] == 1
    assert all(b["count"] == 2 for b in metrics["search"]["modes"]["sparse"]["latency_buckets"])
    assert "secret" not in str(metrics) and first["source"]["id"] not in str(metrics)
    assert client.get("/v1/metrics", headers={"Authorization": "Bearer invalid"}).status_code == 401
    other = client.get(
        "/v1/metrics", headers={"Authorization": "Bearer test-owner-b-token-456"}
    ).json()
    assert other["canonical"] == {"sources": 0, "documents": 0, "chunks": 0}
    assert other["ingestion"]["jobs_by_status"] == {}
    assert all(row["requests"] == 0 for row in other["search"]["modes"].values())
