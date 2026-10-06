"""Hybrid retrieval with canonical evidence, deterministic ranking and no memory decay."""

import hashlib
import re
from collections import Counter
from dataclasses import dataclass
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.orm import load_only

from knowledge_bootstrap.chunking import token_count
from knowledge_bootstrap.index import (
    IndexError,
    SearchIndex,
    index_recipe,
    owned_filter,
    sparse_vector,
)
from knowledge_bootstrap.models import Chunk, Document, IndexConfiguration, Source, Stage
from knowledge_bootstrap.schemas import SearchRequest, SearchResponse, SearchResult
from knowledge_bootstrap.service import ServiceError


def terms(value: str) -> set[str]:
    return set(re.findall(r"[^\W_]+", value.casefold()))


def shingles(value: str) -> set[tuple[str, ...]]:
    words = re.findall(r"[^\W_]+", value.casefold())
    if len(words) < 5:
        return {tuple(words)}
    return {tuple(words[i : i + 5]) for i in range(len(words) - 4)}


@dataclass
class Candidate:
    chunk: Chunk
    document: Document
    source: Source
    components: dict
    signature: set
    relevance: float


def diversify(candidates: list[Candidate], limit: int, debug: bool) -> SearchResponse:
    """Greedy document/section penalties, with near-duplicate evidence removed.

    Penalize repeats rather than imposing a hard cap: searching a single handbook
    can still return several useful sections. Ties always resolve by canonical UUID.
    """
    selected, signatures = [], []
    documents, sections = Counter(), Counter()
    while candidates and len(selected) < limit:

        def adjusted(candidate):
            chunk = candidate.chunk
            section = (chunk.document_id, tuple(chunk.heading_path))
            penalty = 1 / (1 + 0.25 * documents[chunk.document_id] + 0.5 * sections[section])
            return candidate.relevance * penalty, penalty

        candidate = min(candidates, key=lambda c: (-adjusted(c)[0], str(c.chunk.id)))
        candidates.remove(candidate)
        signature = candidate.signature
        if any(
            len(signature & previous) / max(1, len(signature | previous)) >= 0.85
            for previous in signatures
        ):
            continue
        score, penalty = adjusted(candidate)
        chunk, doc, source = candidate.chunk, candidate.document, candidate.source
        selected.append(
            SearchResult(
                chunk_id=chunk.id,
                document_id=doc.id,
                source_id=source.id,
                score=score,
                text=chunk.text,
                title=doc.title,
                heading_path=chunk.heading_path,
                page_start=chunk.page_start,
                page_end=chunk.page_end,
                uri=doc.uri,
                source_uri=source.source_uri,
                format=doc.format,
                score_components={**candidate.components, "diversity_multiplier": penalty}
                if debug
                else None,
            )
        )
        signatures.append(signature)
        documents[doc.id] += 1
        sections[(doc.id, tuple(chunk.heading_path))] += 1
    return SearchResponse(results=selected)


def canonical_candidates(session, settings, owner, request, branches):
    identities = set()
    for points in branches.values():
        for point in points:
            try:
                identities.add(UUID(point["id"]))
            except (ValueError, TypeError, AttributeError):
                continue
    if not identities:
        return []
    statement = (
        select(Chunk, Document, Source, Source.metadata_json["index"])
        .join(Document, Chunk.document_id == Document.id)
        .join(Source, Chunk.source_id == Source.id)
        .where(
            Chunk.id.in_(identities),
            Chunk.owner_id == owner,
            Document.owner_id == owner,
            Source.owner_id == owner,
            Source.status == Stage.READY,
        )
        .options(
            load_only(
                Chunk.id,
                Chunk.document_id,
                Chunk.source_id,
                Chunk.text,
                Chunk.heading_path,
                Chunk.page_start,
                Chunk.page_end,
                Chunk.content_hash,
            ),
            load_only(Document.id, Document.title, Document.uri, Document.format),
            load_only(Source.id, Source.source_uri),
        )
    )
    filters = request.filters
    if filters.source_ids:
        statement = statement.where(Source.id.in_(filters.source_ids))
    if filters.document_ids:
        statement = statement.where(Document.id.in_(filters.document_ids))
    if filters.formats:
        statement = statement.where(Document.format.in_(filters.formats))
    rows = {str(c.id): (c, d, s, projection) for c, d, s, projection in session.execute(statement)}
    rankings = {}
    for name, points in branches.items():
        seen = set()
        for rank, point in enumerate(points, 1):
            try:
                identity = str(UUID(point["id"]))
            except (ValueError, TypeError, AttributeError):
                continue
            if identity not in rows or identity in seen:
                continue
            chunk, doc, source, projection = rows[identity]
            payload = point["payload"]
            projection = projection if isinstance(projection, dict) else {}
            if (
                projection.get("collection") != settings.qdrant_collection
                or projection.get("recipe") != index_recipe(settings)
                or not projection.get("generation")
                or any(
                    payload.get(key) != expected
                    for key, expected in {
                        "kind": "knowledge",
                        "owner_id": owner,
                        "chunk_id": identity,
                        "document_id": str(doc.id),
                        "source_id": str(source.id),
                        "content_hash": chunk.content_hash,
                        "projection_generation": projection["generation"],
                    }.items()
                )
            ):
                continue
            seen.add(identity)
            rankings.setdefault(identity, {})[name] = (rank, point["score"])
    query_terms = terms(request.query)
    identifiers = {term for term in query_terms if any(character.isdigit() for character in term)}
    candidates = []
    for identity, ranks in rankings.items():
        chunk, doc, source, _ = rows[identity]
        # Equal-weight RRF, k=60, normalized to a maximum of 1 for the requested modes.
        modes = 2 if request.mode == "hybrid" else 1
        fusion = sum(61 / (60 + rank) for rank, _ in ranks.values()) / modes
        title = len(query_terms & terms(doc.title)) / max(1, len(query_terms))
        heading = len(query_terms & terms(" ".join(chunk.heading_path))) / max(1, len(query_terms))
        identifier = len(identifiers & terms(chunk.text + " " + doc.title)) / max(
            1, len(identifiers)
        )
        # Exact status/error/worker numbers should survive generic title matches.
        fusion_weight = 0.72 if identifiers else 0.82
        relevance = fusion_weight * fusion + 0.12 * title + 0.06 * heading + 0.10 * identifier
        components = {
            "dense_rank": ranks.get("dense", (None, None))[0],
            "dense_score": ranks.get("dense", (None, None))[1],
            "sparse_rank": ranks.get("sparse", (None, None))[0],
            "sparse_score": ranks.get("sparse", (None, None))[1],
            "fusion": fusion,
            "title_overlap": title,
            "heading_overlap": heading,
            "identifier_overlap": identifier,
            "relevance": relevance,
        }
        candidates.append(
            Candidate(chunk, doc, source, components, shingles(chunk.text), relevance)
        )
    return candidates


def search(session, settings, owner: str, request: SearchRequest, index_factory=None):
    index_factory = index_factory or SearchIndex
    if request.limit > settings.search_max_limit:
        raise ServiceError(
            "search_limit_exceeded", f"limit must be at most {settings.search_max_limit}", 422
        )
    if token_count(request.query) > 1024:
        raise ServiceError("query_too_large", "Query exceeds the 1,024-token limit", 422)
    ready = session.scalar(
        select(Source.id)
        .where(
            Source.owner_id == owner,
            Source.status == Stage.READY,
            Source.metadata_json["index"]["collection"].astext == settings.qdrant_collection,
        )
        .limit(1)
    )
    if ready is None:
        return SearchResponse(results=[])
    target = hashlib.sha256(
        (settings.qdrant_url + "/" + settings.qdrant_collection).encode()
    ).hexdigest()
    manifest = session.get(IndexConfiguration, target)
    if manifest is None or manifest.config_json != index_recipe(settings):
        raise ServiceError(
            "index_configuration_mismatch",
            "Select the indexed embedding/lexical recipe or rebuild into a new collection",
            503,
        )
    # Release the read-only transaction/connection during bounded backend I/O.
    session.rollback()
    filter = owned_filter(owner)
    for key, values in (
        ("source_id", request.filters.source_ids),
        ("document_id", request.filters.document_ids),
        ("format", request.filters.formats),
    ):
        if values:
            filter["must"].append({"key": key, "match": {"any": [str(v) for v in values]}})
    try:
        with index_factory(settings, timeout_seconds=settings.search_timeout_seconds) as index:
            vectors = {}
            if request.mode != "sparse":
                vectors["dense"] = index.embed([request.query])[0]
            if request.mode != "dense":
                lexical = sparse_vector(request.query)
                if lexical["indices"]:
                    vectors["sparse"] = lexical
            branches = index.query(vectors, filter, settings.search_candidate_limit)
    except IndexError as exc:
        code = "search_timeout" if exc.code == "index_timeout" else "search_unavailable"
        raise ServiceError(
            code, "Configured search or embedding backend is unavailable or incompatible", 503
        ) from None
    candidates = canonical_candidates(session, settings, owner, request, branches)
    return diversify(candidates, request.limit, request.debug)
