import json
import math

import httpx
import pytest

from knowledge_bootstrap.index import IndexError, SearchIndex, owned_filter, sparse_vector


def index_settings(settings):
    return settings.model_copy(
        update={
            "embedding_dimensions": 3,
            "embedding_base_url": "https://embeddings.example/v1",
            "qdrant_url": "https://index.example",
            "indexing_enabled": True,
        }
    )


def test_unicode_lexical_representation():
    a = sparse_vector("Deploy deploy café 12")
    b = sparse_vector("12 CAFÉ DEPLOY deploy")
    assert a == b and len(a["indices"]) == 3
    assert a["indices"] == sorted(set(a["indices"]))
    assert 1 + math.log(2) in a["values"]
    assert sparse_vector("?!") == {"indices": [], "values": []}
    assert sparse_vector("café") != sparse_vector("cafe")


def test_embeddings_batch_order_credentials_and_dimension_request(settings):
    from pydantic import SecretStr

    config = index_settings(settings).model_copy(
        update={
            "embedding_api_key": SecretStr("embedding-secret"),
            "embedding_request_dimensions": 3,
        }
    )

    def handler(request):
        assert request.url == "https://embeddings.example/v1/embeddings"
        assert request.headers["Authorization"] == "Bearer embedding-secret"
        assert "api-key" not in request.headers
        body = json.loads(request.content)
        assert body["input"] == ["first", "second"] and body["dimensions"] == 3
        assert body["encoding_format"] == "float"
        return httpx.Response(
            200,
            json={
                "data": [{"index": 1, "embedding": [4, 5, 6]}, {"index": 0, "embedding": [1, 2, 3]}]
            },
        )

    with SearchIndex(config, transport=httpx.MockTransport(handler)) as index:
        assert index.embed(["first", "second"]) == [[1, 2, 3], [4, 5, 6]]


@pytest.mark.parametrize(
    "data",
    [
        None,
        [],
        {},
        [{"index": 0, "embedding": []}],
        [{"index": 0, "embedding": [1, 2]}],
        [{"index": 0, "embedding": [0, 0, 0]}],
        [{"index": 0, "embedding": [True, 1, 2]}],
        [{"index": 0, "embedding": ["a", 1, 2]}],
        [{"index": 4, "embedding": [1, 2, 3]}],
        [{"index": True, "embedding": [1, 2, 3]}],
        [{"embedding": [1, 2, 3]}],
    ],
)
def test_invalid_embeddings(settings, data):
    with SearchIndex(
        index_settings(settings),
        transport=httpx.MockTransport(lambda _: httpx.Response(200, json={"data": data})),
    ) as index:
        with pytest.raises(IndexError) as exc:
            index.embed(["first"])
        assert exc.value.code == "embedding_invalid"


def test_duplicate_indices_and_nonfinite_embeddings(settings):
    for body in [
        b'{"data":[{"index":0,"embedding":[NaN,1,2]}]}',
        b'{"data":[{"index":0,"embedding":[1,2,3]},{"index":0,"embedding":[4,5,6]}]}',
    ]:
        with SearchIndex(
            index_settings(settings),
            transport=httpx.MockTransport(lambda _, body=body: httpx.Response(200, content=body)),
        ) as index:
            with pytest.raises(IndexError):
                index.embed(["a", "b"] if b'index":0' in body[40:] else ["a"])


def collection(config):
    return {
        "result": {
            "config": {
                "params": {
                    "vectors": {
                        "dense": {"size": config.embedding_dimensions, "distance": "Cosine"}
                    },
                    "sparse_vectors": {"sparse": {"modifier": "idf"}},
                }
            }
        }
    }


def test_collection_create_schema_and_completed_upserts(settings):
    config = index_settings(settings)
    requests = []

    def handler(request):
        requests.append(request)
        if request.method == "GET":
            return (
                httpx.Response(404)
                if len(requests) == 1
                else httpx.Response(200, json=collection(config))
            )
        return httpx.Response(200, json={"result": {"status": "completed"}})

    with SearchIndex(config, transport=httpx.MockTransport(handler)) as index:
        index.ensure_collection()
        index.upsert([{"id": "test"}])
        index.delete_filter(owned_filter("owner-a", "source-a"))
    created = json.loads(requests[1].content)
    assert created["sparse_vectors"]["sparse"]["modifier"] == "idf"
    assert created["vectors"]["dense"]["size"] == 3
    assert requests[-2].url.params["wait"] == requests[-1].url.params["wait"] == "true"
    assert len([r for r in requests if r.url.path.endswith("/index")]) == 5
    assert json.loads(requests[-1].content)["filter"]["must"][1]["match"]["value"] == "owner-a"


@pytest.mark.parametrize(
    "params",
    [
        {},
        {
            "vectors": {"dense": {"size": 4, "distance": "Cosine"}},
            "sparse_vectors": {"sparse": {"modifier": "idf"}},
        },
        {"vectors": {"dense": {"size": 3, "distance": "Cosine"}}, "sparse_vectors": {"sparse": {}}},
    ],
)
def test_collection_schema_mismatch_never_mutates(settings, params):
    requests = []

    def handler(request):
        requests.append(request)
        return httpx.Response(200, json={"result": {"config": {"params": params}}})

    with SearchIndex(index_settings(settings), transport=httpx.MockTransport(handler)) as index:
        with pytest.raises(IndexError) as exc:
            index.ensure_collection()
    assert exc.value.code == "index_configuration_mismatch"
    assert len(requests) == 1 and requests[0].method == "GET"


@pytest.mark.parametrize(
    "response,code",
    [
        (httpx.Response(503, text="private upstream details"), "index_write_failed"),
        (httpx.Response(302, headers={"Location": "http://private/"}), "index_write_failed"),
        (httpx.Response(200, text="not json"), "index_write_failed"),
        (httpx.Response(200, json=[]), "index_write_failed"),
        (
            httpx.Response(200, json={"result": {"status": "acknowledged"}}),
            "index_write_incomplete",
        ),
        (httpx.Response(200, content=b"x" * 16_777_217), "index_write_failed"),
    ],
)
def test_backend_errors_bounded_and_sanitized(settings, response, code):
    with SearchIndex(
        index_settings(settings), transport=httpx.MockTransport(lambda _: response)
    ) as index:
        with pytest.raises(IndexError) as exc:
            index.upsert([])
        assert exc.value.code == code and "private" not in exc.value.detail


def test_timeout_and_credential_separation(settings):
    from pydantic import SecretStr

    config = index_settings(settings).model_copy(
        update={
            "embedding_api_key": SecretStr("embed-secret"),
            "qdrant_api_key": SecretStr("index-secret"),
        }
    )

    def handler(request):
        assert request.headers["api-key"] == "index-secret"
        assert "Authorization" not in request.headers
        raise httpx.ReadTimeout("sensitive details")

    with SearchIndex(config, transport=httpx.MockTransport(handler)) as index:
        with pytest.raises(IndexError) as exc:
            index.ensure_collection()
        assert exc.value.code == "index_timeout"
        index.deadline = 0
        with pytest.raises(IndexError) as exc:
            index.embed(["text"])
        assert exc.value.code == "index_timeout"
