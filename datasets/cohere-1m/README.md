# Cohere Wikipedia 1M — Vector Benchmark (fits-in-memory)

The 1m slice of the Cohere corpus: ParadeDB (pg_search vector index) vs
PostgreSQL + pgvector HNSW, every container capped at the same 8g. At this
scale both indexes fit fully in cache, making this the complement to
`cohere-10m`'s data-larger-than-memory regime.

Loads target the `benchmark_1m` database so the 10m tables in `benchmark`
survive on the same containers. `post.sql` recall knobs are set via
`ALTER DATABASE benchmark_1m`.

## Getting the data

With configuration already in the checkout:

```bash
AWS_PROFILE=ci ./bin/loader pull --dataset cohere-1m
```

The manifest points to
`s3://paradedb-benchmark/datasets/cohere/v1/sampled/1m/parquet/cohere_wiki/`
with corpus shards placed directly under `data/`. Query assets are not downloaded
by the manifest. Existing nonempty corpus directories are never overwritten.

To fetch configuration too, use an absent or empty destination:

```bash
AWS_PROFILE=ci ./bin/loader pull --dataset cohere-1m-copy \
  --source s3://paradedb-benchmark/benchmarker/cohere-1m/
```

Query inputs are only needed when regenerating JSON fixtures. Fetch them
separately if they are not already available:

```bash
mkdir -p datasets/cohere-1m/queries
AWS_PROFILE=ci aws s3 cp s3://paradedb-benchmark/datasets/cohere/v1/queries/cohere_queries.parquet \
  datasets/cohere-1m/queries/cohere_queries.parquet
AWS_PROFILE=ci aws s3 cp s3://paradedb-benchmark/datasets/cohere/v1/queries/ground_truth_knn_top10_unfiltered_1m.parquet \
  datasets/cohere-1m/queries/ground_truth_knn_top10_unfiltered_1m.parquet
AWS_PROFILE=ci aws s3 cp s3://paradedb-benchmark/datasets/cohere/v1/queries/ground_truth_knn_top10_1pct_1m.parquet \
  datasets/cohere-1m/queries/ground_truth_knn_top10_1pct_1m.parquet
```

Generate matching k6 vectors and recall JSON explicitly (requires `pyarrow`):

```bash
cd datasets/cohere-1m/k6
python3 make_query_vectors.py
```

The generator checks 1-based query IDs and 1024-dimensional vectors. The checked-in
`ground_truth_top10_1m.json` matches the canonical unfiltered ground truth.
Regenerate the filtered fixture with the same generator and
`--ground-truth ../queries/ground_truth_knn_top10_1pct_1m.parquet
--ground-truth-out ground_truth_top10_1pct_1m.json`.

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

The following ES/ParadeDB figures are historical measurements from the previous
index build. The imported single-segment ParadeDB setup now uses probe `0.0156`
(the source checkout records 95.0% recall); remeasure before comparing latency.

Unfiltered — ES: 40 → 0.890, 80 → 0.949, 95 → 0.961, 110 → 0.962,
150 → 0.975. ParadeDB: 0.03 → 0.954, **0.035 → 0.959**, 0.04 → 0.964.
Previously matched defaults: ES `num_candidates=95` (0.961) vs
`paradedb.vector_cluster_max_probe=0.035` (0.959).

Filtered (1%-selective `text` filter, 'battle'): both engines saturate —
ES 0.999 at `num_candidates=20` / 1.000 from 40; ParadeDB 0.999 at
`max_probe=0.02` / 1.000 from 0.05. The filter leaves ~10k eligible docs,
so top-10 recall is effectively exact on both sides. Defaults pin the
matched 0.999 pair (ES 20, pdb 0.02) — the leanest measured points, so
neither engine pays for recall headroom the comparison can't see.

pgvector is excluded from the 1m comparison (HNSW build wants the whole
~5GB graph in maintenance_work_mem and was cut rather than sized around);
ES runs unmerged (33 segments) since the single-segment force-merge rebuilds
the HNSW graph and costs more than the load itself.

## VectorChord

The `vectorchord` backend on the `postgres` driver uses the same Parquet data with a cosine `vchordrq`
index. The root Compose profile exposes it on port 5435, with its own database
and volume:

```bash
docker compose --profile vectorchord up -d
./bin/loader load --backend vectorchord --workers 4 --batch-size 2000 ./datasets/cohere-1m
VECTORCHORD_URL="postgres://postgres:postgres@localhost:5435/benchmark?options=-c%20vchordrq.probes%3D42" \
./k6 run datasets/cohere-1m/k6/vectorchord.js
```

`post.sql` uses 2,000 partitions and sets `vchordrq.probes=42` for new database
connections. With VectorChord 1.1.1, this measured **95.0% recall@10** across the
100 supplied queries (950 of 1,000 ground-truth neighbors). Nearby settings:

| Probes | Recall@10 |
| ------ | --------- |
| 40     | 94.6%     |
| 41     | 94.6%     |
| 42     | 95.0%     |
| 43     | 95.2%     |
| 44     | 95.4%     |
| 45     | 95.5%     |

These measurements apply to this index build and query set. Remeasure after
rebuilding, since partition training can change recall:

```bash
VECTORCHORD_URL="postgres://postgres:postgres@localhost:5435/benchmark" \
python3 datasets/cohere-1m/k6/measure_recall_vectorchord.py --probes 40,41,42,43,44,45
```

The script uses the dataset's 100
query vectors and checks that every query succeeds and returns ten neighbors.
Set `DURATION` and `VUS` to adjust the run; use `VECTORCHORD_CONTAINER` to select
the container for resource metrics, or an empty string for a remote database.

## PostgreSQL backend comparison

ParadeDB, Timescale pg_vectorsearch, and VectorChord all use the `postgres`
driver. Their manifests in `backends/` define connection/container defaults,
extension settings capture, metadata queries, and index telemetry. Select them
with `--backend`; each name resolves its manifest.

The dataset's `paradedb/backend.yaml` selects `benchmark_1m` and
`vectorchord/backend.yaml` preserves the 42-probe connection default. The
`PARADEDB_URL`, `PG_VECTORSEARCH_URL`, and `VECTORCHORD_URL` environment variables
override those defaults for both loading and k6 runs.
Create `benchmark_1m` on ParadeDB before loading, as in the setup above.

```bash
# From the repository root; all three services have independent data volumes.
docker compose --profile paradedb --profile pg_vectorsearch --profile vectorchord up -d --build
./bin/loader load --backend paradedb ./datasets/cohere-1m
./bin/loader load --backend pg_vectorsearch --workers 4 --batch-size 2000 ./datasets/cohere-1m
./bin/loader load --backend vectorchord --workers 4 --batch-size 2000 ./datasets/cohere-1m
./k6 run --out dashboard datasets/cohere-1m/k6/postgres-backends.js
```

The comparison runs the three strategies sequentially with separate aliases,
query metrics, and extension telemetry. Each backend block uses
`{ type: "<name>" }`. Set `DURATION` and `VUS` to adjust
the run. Set the corresponding `*_CONTAINER` variable to an empty string to
disable Docker metrics for a remote service.

`pg_vectorsearch` is Timescale's **pg_vectorsearch / PRISM**, not pgvectorscale /
DiskANN. The Dockerfile builds the pinned revision from the source checkout.
Use `k6/pg_vectorsearch.js` for a standalone run and
`k6/measure_recall_pg_vectorsearch.py` to measure its recall. Its `PG_VECTORSEARCH_URL`
can include session options such as `options=-c%20prism.nprobe%3D40`.
Remeasure recall for all strategies after rebuilding or changing index settings;
the comparison script checks query success and result counts, not recall parity.
