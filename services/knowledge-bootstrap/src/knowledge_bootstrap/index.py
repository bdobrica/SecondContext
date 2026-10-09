"""Bounded OpenAI-compatible embeddings and a rebuildable Qdrant projection."""

import hashlib
import json
import math
import re
import time
from collections import Counter
from contextlib import AbstractContextManager

import httpx

from knowledge_bootstrap.config import Settings


class IndexError(Exception):
    def __init__(self, code: str, detail: str):
        self.code, self.detail = code, detail
        super().__init__(detail)


def sparse_vector(text: str) -> dict:
    """Unicode lexical log-TF with stable 32-bit hashes; Qdrant supplies IDF.

    No learned model/vocabulary download is required. Hash collisions are possible;
    the same versioned function must encode documents and queries.
    """
    terms = re.findall(r"[^\W_]+", text.casefold(), flags=re.UNICODE)
    counts = Counter(
        int.from_bytes(hashlib.sha256(term.encode()).digest()[:4], "big") for term in terms
    )
    indices = sorted(counts)
    return {"indices": indices, "values": [1 + math.log(counts[i]) for i in indices]}


def index_recipe(settings: Settings) -> dict:
    return {
        "version": 1,
        "embedding_base_url": settings.embedding_base_url,
        "embedding_model": settings.embedding_model,
        "dimensions": settings.embedding_dimensions,
        "request_dimensions": settings.embedding_request_dimensions,
        "sparse": "unicode-sha256-32-logtf-idf-v1",
        "input": "title-heading-text-v1",
    }


class SearchIndex(AbstractContextManager):
    def __init__(self, settings: Settings, *, transport=None, timeout_seconds=None):
        self.settings = settings
        self.deadline = time.monotonic() + (
            settings.index_source_timeout_seconds if timeout_seconds is None else timeout_seconds
        )
        self.client = httpx.Client(transport=transport, trust_env=False, follow_redirects=False)
        self.collection_path = settings.qdrant_url + "/collections/" + settings.qdrant_collection

    def __exit__(self, *args):
        self.client.close()

    def request(self, method, url, body=None, *, embeddings=False, allow_missing=False):
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise IndexError("index_timeout", "Projection exceeded its elapsed-time limit")
        headers = {"Accept-Encoding": "identity"}
        if embeddings:
            key = self.settings.embedding_api_key.get_secret_value()
            if key:
                headers["Authorization"] = "Bearer " + key
        else:
            key = self.settings.qdrant_api_key.get_secret_value()
            if key:
                headers["api-key"] = key
        code = "embedding_failed" if embeddings else "index_write_failed"
        try:
            with self.client.stream(
                method,
                url,
                json=body,
                headers=headers,
                timeout=min(remaining, self.settings.index_timeout_seconds),
            ) as response:
                if allow_missing and response.status_code == 404:
                    return None
                if not 200 <= response.status_code < 300:
                    raise IndexError(code, "Configured backend rejected the request")
                if response.headers.get("Content-Encoding", "identity").lower() != "identity":
                    raise IndexError(code, "Compressed backend responses are not supported")
                raw = bytearray()
                for part in response.iter_bytes():
                    if time.monotonic() > self.deadline:
                        raise IndexError("index_timeout", "Projection exceeded its time limit")
                    raw.extend(part)
                    if len(raw) > 16_777_216:
                        raise IndexError(code, "Backend response exceeded the byte limit")
                result = json.loads(raw)
                if not isinstance(result, dict):
                    raise IndexError(code, "Backend response must be a JSON object")
                return result
        except httpx.TimeoutException:
            raise IndexError("index_timeout", "Configured backend timed out") from None
        except (httpx.HTTPError, ValueError, TypeError):
            raise IndexError(
                code, "Configured backend is unavailable or returned invalid data"
            ) from None

    def ensure_collection(self):
        result = self.request("GET", self.collection_path, allow_missing=True)
        if result is None:
            try:
                self.request(
                    "PUT",
                    self.collection_path,
                    {
                        "vectors": {
                            "dense": {
                                "size": self.settings.embedding_dimensions,
                                "distance": "Cosine",
                            }
                        },
                        "sparse_vectors": {"sparse": {"modifier": "idf"}},
                    },
                )
            except IndexError:
                # Another replica may have created it. Always validate the resulting schema.
                result = self.request("GET", self.collection_path)
            else:
                result = self.request("GET", self.collection_path)
        try:
            params = result["result"]["config"]["params"]
            dense = params["vectors"]
            sparse = params["sparse_vectors"]
            compatible = (
                set(dense) == {"dense"}
                and dense["dense"]["size"] == self.settings.embedding_dimensions
                and dense["dense"]["distance"] == "Cosine"
                and set(sparse) == {"sparse"}
                and sparse["sparse"]["modifier"] == "idf"
            )
        except (KeyError, TypeError):
            compatible = False
        if not compatible:
            raise IndexError(
                "index_configuration_mismatch", "Use a dedicated compatible collection"
            )
        for key in ("owner_id", "source_id", "document_id", "kind", "projection_generation"):
            self.request(
                "PUT",
                self.collection_path + "/index?wait=true",
                {
                    "field_name": key,
                    "field_schema": "keyword",
                },
            )

    def embed(self, texts: list[str]) -> list[list[float]]:
        body = {"model": self.settings.embedding_model, "input": texts, "encoding_format": "float"}
        if self.settings.embedding_request_dimensions is not None:
            body["dimensions"] = self.settings.embedding_request_dimensions
        result = self.request(
            "POST",
            self.settings.embedding_base_url + "/embeddings",
            body,
            embeddings=True,
        )
        try:
            data = result["data"]
            if not isinstance(data, list) or len(data) != len(texts):
                raise ValueError
            vectors = [None] * len(texts)
            for item in data:
                i, vector = item["index"], item["embedding"]
                if type(i) is not int or not 0 <= i < len(texts) or vectors[i] is not None:
                    raise ValueError
                if (
                    not isinstance(vector, list)
                    or len(vector) != self.settings.embedding_dimensions
                    or any(type(v) not in (int, float) or not math.isfinite(v) for v in vector)
                    or not any(vector)
                ):
                    raise ValueError
                vectors[i] = vector
            return vectors
        except (KeyError, TypeError, ValueError, OverflowError):
            raise IndexError(
                "embedding_invalid", "Embedding response has invalid vectors or dimensions"
            ) from None

    def mutate(self, method, path, body, *, allow_missing=False):
        result = self.request(
            method, self.collection_path + path + "?wait=true", body, allow_missing=allow_missing
        )
        if result is None and allow_missing:
            return
        acknowledgement = result.get("result")
        if not isinstance(acknowledgement, dict) or acknowledgement.get("status") != "completed":
            raise IndexError(
                "index_write_incomplete", "Qdrant did not acknowledge a completed write"
            )

    def upsert(self, points):
        self.mutate("PUT", "/points", {"points": points})

    def delete_filter(self, filter, *, allow_missing=False):
        self.mutate("POST", "/points/delete", {"filter": filter}, allow_missing=allow_missing)

    def query(self, vectors: dict, filter: dict, limit: int) -> dict[str, list[dict]]:
        """Read named vectors in one bounded request; never create or modify an index."""
        if not vectors:
            return {}
        result = self.request(
            "POST",
            self.collection_path + "/points/query/batch",
            {
                "searches": [
                    {
                        "query": vector,
                        "using": name,
                        "filter": filter,
                        "limit": limit,
                        "with_payload": True,
                        "with_vector": False,
                    }
                    for name, vector in vectors.items()
                ]
            },
        )
        try:
            batches = result["result"]
            if not isinstance(batches, list) or len(batches) != len(vectors):
                raise ValueError
            output = {}
            for name, batch in zip(vectors, batches, strict=True):
                points = batch["points"]
                if not isinstance(points, list) or len(points) > limit:
                    raise ValueError
                for point in points:
                    if (
                        not isinstance(point.get("id"), str)
                        or not isinstance(point.get("payload"), dict)
                        or type(point.get("score")) not in (int, float)
                        or not math.isfinite(point["score"])
                    ):
                        raise ValueError
                # Equal backend scores otherwise depend on insertion/rebuild order,
                # which changes reciprocal ranks even though the evidence is identical.
                output[name] = sorted(points, key=lambda p: (-p["score"], p["id"]))
            return output
        except (KeyError, TypeError, ValueError, AttributeError, OverflowError):
            raise IndexError(
                "search_invalid", "Search backend returned invalid candidates"
            ) from None


def owned_filter(owner: str, source_id=None):
    must = [
        {"key": "kind", "match": {"value": "knowledge"}},
        {"key": "owner_id", "match": {"value": owner}},
    ]
    if source_id is not None:
        must.append({"key": "source_id", "match": {"value": str(source_id)}})
    return {"must": must}
