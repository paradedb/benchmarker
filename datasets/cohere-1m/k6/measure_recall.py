#!/usr/bin/env python3
"""Measure Elasticsearch recall@10 for the cohere_wiki kNN benchmark.

Runs every held-out query vector through the same kNN request shape that
k6/vector.js benchmarks, at each --candidates value, and intersects the
returned document ids with the exact top-10 in ground_truth_top10_10m.json.
Matched-recall operating points across backends are a precondition for
comparing ANN latency at all; this reports the ES side of that tuning.

Stdlib only (no pyarrow): ground truth ships as JSON in this directory,
generated from queries/ground_truth_knn_top10_unfiltered_10m.parquet with
query_id 1..N matching the row order of query_vectors.json.

Usage:
    python3 measure_recall.py [--url http://localhost:9200] \
        [--index cohere_wiki] [--candidates 10,20,40,80,150,300,600,1000]
"""

import argparse
import json
import time
import urllib.request


def knn_ids(url, index, vector, k, num_candidates, filter_term=None):
    knn = {
        "field": "emb",
        "query_vector": vector,
        "k": k,
        "num_candidates": num_candidates,
    }
    if filter_term:
        knn["filter"] = {"match": {"text": filter_term}}
    body = json.dumps({"knn": knn, "size": k, "_source": False}).encode()
    req = urllib.request.Request(
        f"{url}/{index}/_search?request_cache=false",
        body,
        {"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req) as resp:
        hits = json.load(resp)["hits"]["hits"]
    return [h["_id"] for h in hits]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://localhost:9200")
    parser.add_argument("--index", default="cohere_wiki")
    parser.add_argument("--vectors", default="query_vectors.json")
    parser.add_argument("--ground-truth", default="ground_truth_top10_10m.json")
    parser.add_argument("--candidates", default="10,20,40,80,150,300,600,1000")
    parser.add_argument("--filter-term", help="full-text filter gating the kNN (1pct shape)")
    args = parser.parse_args()

    with open(args.vectors) as f:
        vectors = [json.loads(v) for v in json.load(f)]
    with open(args.ground_truth) as f:
        truth = {row["query_id"]: set(row["gt_ids"]) for row in json.load(f)}

    k = len(next(iter(truth.values())))
    print(f"{len(vectors)} queries, recall@{k}, index {args.index}")
    print(f"{'num_candidates':>14} {'recall@' + str(k):>10} {'avg_ms':>8}")

    for n in [int(c) for c in args.candidates.split(",")]:
        overlap = 0
        start = time.time()
        for qid, vector in enumerate(vectors, start=1):
            ids = knn_ids(args.url, args.index, vector, k, n, args.filter_term)
            overlap += len(set(ids) & truth[qid])
        avg_ms = (time.time() - start) * 1000 / len(vectors)
        recall = overlap / (len(vectors) * k)
        print(f"{n:>14} {recall:>10.4f} {avg_ms:>8.1f}")


if __name__ == "__main__":
    main()
