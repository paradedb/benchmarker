#!/usr/bin/env python3
"""Measure recall@k for the SIFT kNN benchmark, per backend.

k6 only times queries; it never checks which ids come back. This script runs
the same query shape that k6/vector.js benchmarks, records the returned ids,
and scores them against the exact top-k ground truth.

  collect  Run each held-out query vector against one backend and write
           results/<backend>.json as {"<query_id>": [ids, ...]}.
  compare  Score one or more results files against ground_truth_top10.json.

Query ids are 0-based positions in query_vectors.json (see
make_query_vectors.py). Stdlib only; paradedb is reached through
`docker exec psql` (or --psql) and elasticsearch over HTTP, so no driver is
needed on the host.

Usage:
    python3 measure_recall.py collect --backend paradedb [--limit 1000] \
        [--set paradedb.vector_cluster_max_probe=0.02]
    python3 measure_recall.py collect --backend elasticsearch \
        [--num-candidates 100] [--out results/es-100.json]
    python3 measure_recall.py compare results/paradedb.json results/elasticsearch.json
"""

import argparse
import json
import os
import subprocess
import time
import urllib.request

K = 10

# Keep in sync with PARADEDB_KNN in vector.js.
PARADEDB_KNN = (
    "SELECT _id FROM sift WHERE _id @@@ paradedb.all() "
    "ORDER BY emb <-> '{vector}'::vector(128) LIMIT {k};"
)


def collect_paradedb(args, vectors):
    if args.psql:
        psql_cmd = args.psql.split() + ["-d", args.db]
    else:
        psql_cmd = [
            "docker", "exec", "-i", args.container,
            "psql", "-U", args.user, "-d", args.db,
        ]

    lines = []
    for setting in args.set:
        name, value = setting.split("=", 1)
        lines.append(f"SET {name} TO '{value}';")
    for qid, vector in enumerate(vectors):
        lines.append(f"\\echo Q:{qid}")
        lines.append(PARADEDB_KNN.format(vector=vector, k=K))

    out = subprocess.run(
        psql_cmd + ["-tA", "-v", "ON_ERROR_STOP=1"],
        input="\n".join(lines),
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


def collect_elasticsearch(args, vectors):
    # Keep in sync with elasticsearchKnn in vector.js.
    results = {}
    for qid, vector in enumerate(vectors):
        body = json.dumps({
            "knn": {
                "field": "emb",
                "query_vector": json.loads(vector),
                "k": K,
                "num_candidates": args.num_candidates,
            },
            "size": K,
            "_source": False,
        }).encode()
        req = urllib.request.Request(
            f"{args.url}/{args.index}/_search?request_cache=false",
            body,
            {"Content-Type": "application/json"},
        )
        with urllib.request.urlopen(req) as resp:
            results[qid] = [hit["_id"] for hit in json.load(resp)["hits"]["hits"]]
    return results


ADAPTERS = {
    "paradedb": collect_paradedb,
    "elasticsearch": collect_elasticsearch,
}


def collect(args):
    with open(args.vectors) as f:
        vectors = json.load(f)[: args.limit]

    start = time.time()
    results = ADAPTERS[args.backend](args, vectors)
    elapsed = time.time() - start
    if len(results) != len(vectors):
        raise SystemExit(f"expected {len(vectors)} results, got {len(results)}")

    out = args.out or os.path.join("results", f"{args.backend}.json")
    os.makedirs(os.path.dirname(out) or ".", exist_ok=True)
    with open(out, "w") as f:
        json.dump({str(qid): ids for qid, ids in sorted(results.items())}, f)
    print(
        f"{args.backend}: {len(results)} queries in {elapsed:.1f}s "
        f"({elapsed * 1000 / len(results):.1f} ms avg, unbenchmarked) -> {out}"
    )


def compare(args):
    with open(args.ground_truth) as f:
        truth = {row["query_id"]: set(row["gt_ids"][:K]) for row in json.load(f)}

    print(f"{'results':<30} {'queries':>8} {'recall@' + str(K):>10} {'min':>6}")
    for path in args.results:
        with open(path) as f:
            results = {int(qid): ids for qid, ids in json.load(f).items()}
        missing = results.keys() - truth.keys()
        if missing:
            raise SystemExit(f"{path}: no ground truth for query ids {sorted(missing)[:5]}")
        per_query = [len(set(ids[:K]) & truth[qid]) / K for qid, ids in results.items()]
        recall = sum(per_query) / len(per_query)
        print(f"{path:<30} {len(per_query):>8} {recall:>10.4f} {min(per_query):>6.2f}")


def main():
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    sub = parser.add_subparsers(dest="command", required=True)

    c = sub.add_parser("collect", help="record ids returned by a backend")
    c.add_argument("--backend", required=True, choices=sorted(ADAPTERS))
    c.add_argument("--limit", type=int, default=1000, help="number of queries to run")
    c.add_argument("--vectors", default="query_vectors.json")
    c.add_argument("--out", help="default: results/<backend>.json")
    pdb = c.add_argument_group("paradedb")
    pdb.add_argument("--container", default="paradedb")
    pdb.add_argument("--db", default="benchmark")
    pdb.add_argument("--user", default="postgres")
    pdb.add_argument("--psql", help="run this psql command instead of docker exec")
    pdb.add_argument(
        "--set", action="append", default=[], metavar="GUC=VALUE",
        help="session setting override (repeatable)",
    )
    es = c.add_argument_group("elasticsearch")
    es.add_argument("--url", default="http://localhost:9200")
    es.add_argument("--index", default="sift")
    es.add_argument(
        "--num-candidates", type=int, default=100,
        help="match ES_NUM_CANDIDATES in vector.js",
    )
    c.set_defaults(func=collect)

    p = sub.add_parser("compare", help="score results files against ground truth")
    p.add_argument("results", nargs="+")
    p.add_argument("--ground-truth", default="ground_truth_top10.json")
    p.set_defaults(func=compare)

    args = parser.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
