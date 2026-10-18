#!/usr/bin/env python3
"""Measure PRISM recall@10 against Cohere 1M ground truth using psql."""

import argparse
import json
import os
from pathlib import Path
import subprocess


def main():
    root = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--vectors", type=Path, default=root / "query_vectors.json")
    parser.add_argument(
        "--ground-truth", type=Path, default=root / "ground_truth_top10_1m.json"
    )
    parser.add_argument("--probes", default="0,10,40,100")
    args = parser.parse_args()
    probes = [int(value) for value in args.probes.split(",")]
    if any(probe < 0 for probe in probes):
        parser.error("probes must be non-negative integers")

    vectors = json.loads(args.vectors.read_text())
    truth = {
        row["query_id"]: set(row["gt_ids"])
        for row in json.loads(args.ground_truth.read_text())
    }
    if set(truth) != set(range(1, len(vectors) + 1)):
        parser.error("ground truth must contain one entry per query vector")
    if any(len(ids) != 10 for ids in truth.values()):
        parser.error("expected exactly 10 ground truth IDs per query")

    connection = os.environ.get(
        "PG_VECTORSEARCH_URL", "postgres://postgres:postgres@localhost:5434/benchmark"
    )
    print(f"{len(vectors)} held-out queries", flush=True)
    print("nprobe recall@10", flush=True)
    for probe in probes:
        statements = [f"SET prism.nprobe = {probe};"]
        for qid, vector in enumerate(vectors, start=1):
            # Parse and serialize numerical vectors instead of interpolating
            # arbitrary strings from the query file into SQL.
            values = json.loads(vector) if isinstance(vector, str) else vector
            literal = "[" + ",".join(str(float(value)) for value in values) + "]"
            statements.extend([
                f"\\echo Q:{qid}",
                "SELECT _id FROM cohere_wiki "
                f"ORDER BY emb <=> '{literal}'::vector(1024) LIMIT 10;",
            ])
        result = subprocess.run(
            ["psql", "--dbname", connection, "-X", "-tA", "-v", "ON_ERROR_STOP=1"],
            input="\n".join(statements),
            text=True, capture_output=True,
        )
        if result.returncode:
            raise RuntimeError(result.stderr.strip())
        results = {}
        current = None
        for line in result.stdout.splitlines():
            if line.startswith("Q:"):
                current = int(line[2:])
                results[current] = set()
            elif current is not None and line:
                results[current].add(line)
        if set(results) != set(truth) or any(len(ids) != 10 for ids in results.values()):
            raise RuntimeError("expected 10 distinct results for every query")
        overlap = sum(len(results[qid] & ids) for qid, ids in truth.items())
        print(f"{probe:6d} {overlap / (10 * len(truth)):.4f}", flush=True)


if __name__ == "__main__":
    main()
