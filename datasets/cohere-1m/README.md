# Cohere Wikipedia 1M — Vector Benchmark (fits-in-memory)

The 1m slice of the Cohere corpus: ParadeDB (pg_search vector index) vs
PostgreSQL + pgvector HNSW, every container capped at the same 8g. At this
scale both indexes fit fully in cache, making this the complement to
`cohere-10m`'s data-larger-than-memory regime.

Loads target the `benchmark_1m` database so the 10m tables in `benchmark`
survive on the same containers. `post.sql` recall knobs are set via
`ALTER DATABASE benchmark_1m`.

## Getting the data

```bash
./bin/loader pull --dataset cohere-1m --source s3://paradedb-benchmarker/datasets/cohere-1m/
```

Upstream source: `s3://paradedb-benchmarks/datasets/cohere/sampled/1m/parquet/`.

## Load

```bash
docker exec paradedb createdb -U postgres benchmark_1m
docker exec postgres createdb -U postgres benchmark_1m

PARADEDB_URL=postgres://postgres:postgres@localhost:5432/benchmark_1m \
  ./bin/loader load --backend paradedb --workers 4 --batch-size 2000 ./datasets/cohere-1m
POSTGRES_URL=postgres://postgres:postgres@localhost:5433/benchmark_1m \
  ./bin/loader load --backend postgres --workers 4 --batch-size 2000 ./datasets/cohere-1m
```

## Recall

Same 100 held-out query vectors as 10m; ground truth is the 1m variant
(`ground_truth_top10_1m.json`, from
`queries/ground_truth_knn_top10_unfiltered_1m.parquet`). Tune both engines
to the same measured recall@10 before comparing latency:

```bash
cd datasets/cohere-1m/k6
python3 measure_recall_pdb.py --db benchmark_1m --ground-truth ground_truth_top10_1m.json
```

## Run

```bash
./k6 run --out dashboard=live,json,html datasets/cohere-1m/k6/vector.js
```

## Measured recall@10 (1m, 100 held-out queries)

Measured against the current indexes (both cover `text` for the filtered
shape; ES as-loaded multi-segment, no force-merge).

Unfiltered — ES: 40 → 0.890, 80 → 0.949, 95 → 0.961, 110 → 0.962,
150 → 0.975. ParadeDB: 0.03 → 0.954, **0.035 → 0.959**, 0.04 → 0.964.
Matched defaults: ES `num_candidates=95` (0.961) vs
`paradedb.vector_cluster_max_probe=0.035` (0.959).

Filtered (1%-selective `text` filter, 'battle'): both engines saturate —
ES 1.000 from `num_candidates=40`, ParadeDB 1.000 from `max_probe=0.05`
(the defaults). The filter leaves ~10k eligible docs, so top-10 recall is
effectively exact on both sides.

pgvector is excluded from the 1m comparison (HNSW build wants the whole
~5GB graph in maintenance_work_mem and was cut rather than sized around);
ES runs unmerged (33 segments) since the single-segment force-merge rebuilds
the HNSW graph and costs more than the load itself.
