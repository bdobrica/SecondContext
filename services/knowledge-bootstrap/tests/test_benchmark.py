import importlib.util
import json
from pathlib import Path
from uuid import uuid4

import httpx
import pytest

ROOT = Path(__file__).resolve().parents[1] / "benchmarks"
spec = importlib.util.spec_from_file_location("benchmark", ROOT / "evaluate.py")
benchmark = importlib.util.module_from_spec(spec)
spec.loader.exec_module(benchmark)


def test_benchmark_measures_each_mode_with_fixed_denominator_and_provenance():
    corpus = json.loads((ROOT / "corpus.json").read_text())
    sources = {d["key"]: str(uuid4()) for d in corpus["documents"]}
    calls = []

    def respond(request):
        body = json.loads(request.content)
        calls.append(body)
        assert body["filters"]["source_ids"] == list(sources.values())
        query = next(q for q in corpus["queries"] if q["query"] == body["query"])
        relevant = query["relevant"]
        if body["mode"] == "sparse":
            relevant = relevant[:1]
        results = [
            {
                "chunk_id": str(uuid4()),
                "document_id": str(uuid4()),
                "source_id": sources[key],
                "uri": "text://" + sources[key],
                "format": "markdown",
                "text": "Evidence",
                "title": next(d["title"] for d in corpus["documents"] if d["key"] == key),
            }
            for key in relevant
        ]
        return httpx.Response(200, json={"results": results})

    with httpx.Client(base_url="http://test", transport=httpx.MockTransport(respond)) as client:
        report = benchmark.evaluate(client, corpus, sources)
    assert len(calls) == len(corpus["queries"]) * 3
    assert report["means"]["hybrid"]["precision_at_k"] == pytest.approx(13 / 30)
    assert report["means"]["sparse"]["precision_at_k"] == pytest.approx(1 / 3)
    assert report["hybrid_improvements"]["sparse"] == [
        "mixed_recovery",
        "mixed_auth",
        "mixed_restore",
    ]
    assert report["means"]["hybrid"]["precision_at_1"] == 1


def test_benchmark_requires_complete_unique_source_mapping():
    corpus = json.loads((ROOT / "corpus.json").read_text())
    with pytest.raises(ValueError):
        benchmark.evaluate(None, corpus, {})
    with pytest.raises(ValueError):
        benchmark.evaluate(None, corpus, {d["key"]: "same-source" for d in corpus["documents"]})
