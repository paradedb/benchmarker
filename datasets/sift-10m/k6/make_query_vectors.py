#!/usr/bin/env python3
"""Convert the SIFT query and ground-truth parquet files into k6/recall JSON.

Writes:
  query_vectors.json    JSON array of pgvector literals ("[1,2,...]") in
                        query id order, served to k6 through db.terms().
  ground_truth_top10.json  [{"query_id": N, "gt_ids": [...]}, ...] so that
                        measure_recall.py can stay stdlib-only.

Query ids are 0-based and equal to the position in query_vectors.json.
"""

import argparse
import json

import pyarrow.parquet as pq


def vector_literal(values) -> str:
    return "[" + ",".join(f"{float(x):g}" for x in values) + "]"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--queries", default="../queries/sift_queries.parquet")
    parser.add_argument(
        "--ground-truth",
        default="../queries/ground_truth_knn_top10_unfiltered_10m.parquet",
    )
    parser.add_argument("--vectors-out", default="query_vectors.json")
    parser.add_argument("--ground-truth-out", default="ground_truth_top10.json")
    args = parser.parse_args()

    queries = sorted(
        pq.read_table(args.queries, columns=["id", "emb"]).to_pylist(),
        key=lambda row: row["id"],
    )
    if [row["id"] for row in queries] != list(range(len(queries))):
        raise SystemExit("query ids are not a contiguous 0-based range")
    vectors = [vector_literal(row["emb"]) for row in queries]
    with open(args.vectors_out, "w") as f:
        json.dump(vectors, f)
    print(f"Wrote {len(vectors)} query vectors to {args.vectors_out}")

    truth = sorted(
        pq.read_table(args.ground_truth, columns=["query_id", "gt_ids"]).to_pylist(),
        key=lambda row: row["query_id"],
    )
    with open(args.ground_truth_out, "w") as f:
        json.dump(truth, f)
    print(f"Wrote {len(truth)} ground-truth rows to {args.ground_truth_out}")


if __name__ == "__main__":
    main()
