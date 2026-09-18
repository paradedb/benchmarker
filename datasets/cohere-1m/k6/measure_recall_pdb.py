#!/usr/bin/env python3
"""Measure ParadeDB recall@10 for the cohere_wiki kNN benchmark.

Sweeps paradedb.vector_cluster_max_probe, running the same query shape as
k6/vector.js once per held-out vector (one statement per query, vector
inlined — a lateral/aggregate rewrite can change the plan and measure a
query the benchmark never runs). Returned ids are intersected with the
exact top-10 in ground_truth_top10_10m.json.

Stdlib only; talks to postgres through `docker exec psql` (or psql via
--psql), so it runs on the benchmark host with no driver installed.

Usage:
    python3 measure_recall_pdb.py [--container paradedb] [--db benchmark] \
        [--probes 0.01,0.02,0.05,0.1,0.2,0.5]
"""

import argparse
import json
import subprocess
import time


def run_point(psql_cmd, probe, vectors, k, filter_term=None):
    if filter_term:
        where = f"text ||| '{filter_term}'"
    else:
        where = "_id @@@ paradedb.all()"
    lines = [f"SET paradedb.vector_cluster_max_probe TO {probe};"]
    for qid, vec in enumerate(vectors, start=1):
        lines.append(f"\\echo Q:{qid}")
        lines.append(
            f"SELECT _id FROM cohere_wiki WHERE {where} "
            f"ORDER BY emb <=> '{vec}'::vector(1024) LIMIT {k};"
        )
    script = "\n".join(lines)

    out = subprocess.run(
        psql_cmd + ["-tA", "-v", "ON_ERROR_STOP=1"],
        input=script,
        capture_output=True,
        text=True,
        check=True,
    ).stdout

    results = {}
    current = None
    for line in out.splitlines():
        if line.startswith("Q:"):
            current = int(line[2:])
            results[current] = []
        elif line and current is not None:
            results[current].append(line)
    return results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--container", default="paradedb")
    parser.add_argument("--db", default="benchmark")
    parser.add_argument("--user", default="postgres")
    parser.add_argument("--psql", help="run this psql command instead of docker exec")
    parser.add_argument("--vectors", default="query_vectors.json")
    parser.add_argument("--ground-truth", default="ground_truth_top10_10m.json")
    parser.add_argument("--probes", default="0.01,0.02,0.05,0.1,0.2,0.5")
    parser.add_argument("--filter-term", help="full-text filter gating the kNN (1pct shape)")
    args = parser.parse_args()

    if args.psql:
        psql_cmd = args.psql.split() + ["-d", args.db]
    else:
        psql_cmd = [
            "docker", "exec", "-i", args.container,
            "psql", "-U", args.user, "-d", args.db,
        ]

    with open(args.vectors) as f:
        vectors = json.load(f)  # entries are pgvector literals already
    with open(args.ground_truth) as f:
        truth = {row["query_id"]: set(row["gt_ids"]) for row in json.load(f)}

    k = len(next(iter(truth.values())))
    print(f"{len(vectors)} queries, recall@{k}, container {args.container}")
    print(f"{'max_probe':>10} {'recall@' + str(k):>10} {'avg_ms':>8}", flush=True)

    for probe in args.probes.split(","):
        start = time.time()
        results = run_point(psql_cmd, float(probe), vectors, k, args.filter_term)
        avg_ms = (time.time() - start) * 1000 / len(vectors)
        overlap = sum(len(set(ids) & truth[qid]) for qid, ids in results.items())
        recall = overlap / (len(truth) * k)
        print(f"{float(probe):>10} {recall:>10.4f} {avg_ms:>8.1f}", flush=True)


if __name__ == "__main__":
    main()
