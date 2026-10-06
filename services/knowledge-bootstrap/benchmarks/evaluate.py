"""Run an HTTP-only retrieval benchmark against a service with this corpus ingested.

Input is a mapping from corpus keys to source UUIDs; no database or Qdrant access.
The token comes only from KNOWLEDGE_TOKEN. This script never creates/deletes sources.
"""

import argparse
import json
import os
from pathlib import Path

import httpx

MODES = ("dense", "sparse", "hybrid")
CORPUS = Path(__file__).with_name("corpus.json")


def evaluate(client, corpus, sources, k=3):
    expected = {d["key"] for d in corpus["documents"]}
    if set(sources) != expected or len(set(sources.values())) != len(sources):
        raise ValueError("Source mapping must identify every benchmark document uniquely")
    keys = {value: key for key, value in sources.items()}
    measurements = []
    for query in corpus["queries"]:
        row = {"key": query["key"], "query": query["query"], "relevant": query["relevant"]}
        for mode in MODES:
            response = client.post(
                "/v1/search",
                json={
                    "query": query["query"],
                    "limit": k,
                    "mode": mode,
                    "filters": {"source_ids": list(sources.values())},
                },
            )
            response.raise_for_status()
            results = response.json()["results"]
            retrieved = []
            for result in results:
                key = keys[result["source_id"]]
                assert result["chunk_id"] and result["document_id"] and result["uri"]
                assert result["format"] == "markdown" and result["text"]
                assert result["title"] == next(
                    d["title"] for d in corpus["documents"] if d["key"] == key
                )
                retrieved.append(key)
            relevant = set(query["relevant"])
            row[mode] = {
                "precision_at_k": sum(key in relevant for key in retrieved) / k,
                "precision_at_1": float(bool(retrieved) and retrieved[0] in relevant),
                "retrieved": retrieved,
            }
        measurements.append(row)
    means = {
        mode: {
            "precision_at_k": sum(row[mode]["precision_at_k"] for row in measurements)
            / len(measurements),
            "precision_at_1": sum(row[mode]["precision_at_1"] for row in measurements)
            / len(measurements),
        }
        for mode in MODES
    }
    improved = {
        mode: [
            row["key"]
            for row in measurements
            if row["hybrid"]["precision_at_k"] > row[mode]["precision_at_k"]
        ]
        for mode in ("dense", "sparse")
    }
    return {
        "corpus_version": corpus["version"],
        "k": k,
        "means": means,
        "hybrid_improvements": improved,
        "queries": measurements,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8090")
    parser.add_argument("--sources", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--k", type=int, choices=range(1, 21), default=3)
    args = parser.parse_args()
    token = os.environ.get("KNOWLEDGE_TOKEN")
    if not token:
        parser.error("KNOWLEDGE_TOKEN is required")
    with httpx.Client(
        base_url=args.url, headers={"Authorization": "Bearer " + token}, trust_env=False, timeout=30
    ) as client:
        report = evaluate(
            client, json.loads(CORPUS.read_text()), json.loads(args.sources.read_text()), args.k
        )
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    print(
        json.dumps(
            {"means": report["means"], "hybrid_improvements": report["hybrid_improvements"]},
            indent=2,
        )
    )


if __name__ == "__main__":
    main()
