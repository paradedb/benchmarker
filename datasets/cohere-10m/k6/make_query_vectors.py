#!/usr/bin/env python3
"""Generate Cohere k6 vectors and recall fixtures from matching shared Parquet."""

import argparse
import json
from pathlib import Path

import pyarrow.parquet as pq


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", default="../queries/cohere_queries.parquet")
    parser.add_argument("--column", default="emb")
    parser.add_argument("--out", default="query_vectors.json")
    parser.add_argument(
        "--ground-truth",
        default="../queries/ground_truth_knn_top10_unfiltered_10m.parquet",
    )
    parser.add_argument("--ground-truth-out", default="ground_truth_top10_10m.json")
    args = parser.parse_args()

    queries = pq.read_table(args.data).to_pylist()
    # Recall scripts use 1-based query IDs in the original query row order.
    ids = [row["id"] for row in queries]
    if ids != list(range(1, len(queries) + 1)):
        raise SystemExit("query IDs must be a contiguous 1-based range in row order")
    vectors = [
        "[" + ",".join(repr(float(x)) for x in row[args.column]) + "]"
        for row in queries
    ]
    if any(len(row[args.column]) != 1024 for row in queries):
        raise SystemExit("expected 1024-dimensional Cohere query vectors")
    truth = pq.read_table(args.ground_truth, columns=["query_id", "gt_ids"]).to_pylist()
    truth.sort(key=lambda row: row["query_id"])
    if [row["query_id"] for row in truth] != ids:
        raise SystemExit("ground-truth IDs do not match the query set")
    Path(args.out).write_text(json.dumps(vectors))
    Path(args.ground_truth_out).write_text(json.dumps(truth))
    print(f"Wrote {len(vectors)} query vectors and {len(truth)} ground-truth rows")


if __name__ == "__main__":
    main()
