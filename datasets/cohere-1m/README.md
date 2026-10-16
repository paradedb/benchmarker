# Cohere Wikipedia 1M — Vector Benchmark (fits-in-memory)

The 1m slice of the Cohere corpus, comparing ParadeDB, Elasticsearch,
Timescale pg_vectorsearch (PRISM), and VectorChord. Each container is capped
at 4 CPUs and 8 GiB RAM, with its own persistent data volume.

## Start the engines

From the repository root:

```bash
docker compose -f datasets/cohere-1m/docker-compose.yml up -d --build --wait
```

The Compose file uses the pinned images and PostgreSQL tuning from the root
configuration. It builds pg_vectorsearch using `docker/vectorsearch/Dockerfile`.

| Service                          | Host port | Database       |
| -------------------------------- | --------- | -------------- |
| ParadeDB                         | 5432      | `benchmark_1m` |
| Elasticsearch                    | 9200      | —              |
| pg_vectorsearch (`vectorsearch`) | 5434      | `benchmark`    |
| VectorChord                      | 5435      | `benchmark`    |

PostgreSQL credentials are `postgres` / `postgres`. These defaults match the
benchmark profiles. ParadeDB's `benchmark_1m` database is created automatically
on the first start with an empty volume. If reusing another ParadeDB instance,
create that database before loading. The default ports and container names must
be available.

Stop the engines while keeping their data:

```bash
docker compose -f datasets/cohere-1m/docker-compose.yml down
```

## Getting the data

```bash
./bin/loader pull --dataset cohere-1m --source s3://paradedb-benchmarker/datasets/cohere-1m/
```

Upstream source: `s3://paradedb-benchmarks/datasets/cohere/sampled/1m/parquet/`.

## Load

```bash
./bin/loader load --backend paradedb --workers 4 --batch-size 2000 ./datasets/cohere-1m
./bin/loader load --backend elasticsearch --workers 4 --batch-size 2000 ./datasets/cohere-1m
./bin/loader load --backend vectorsearch --workers 4 --batch-size 2000 ./datasets/cohere-1m
./bin/loader load --backend vectorchord --workers 4 --batch-size 2000 ./datasets/cohere-1m
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

The `vectorchord` profile on the `postgres` driver uses the same Parquet data with a cosine `vchordrq`
index. The dataset Compose file exposes it on port 5435, with its own database
and volume:

```bash
docker compose -f datasets/cohere-1m/docker-compose.yml up -d --wait vectorchord
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

## PostgreSQL profile comparison

ParadeDB, Timescale pg_vectorsearch, and VectorChord all use the `postgres`
driver. Their manifests in `profiles/` define connection/container defaults,
extension settings capture, metadata queries, and index telemetry. Select them
with the existing `--backend` flag; both resolve the manifest.

The dataset's `paradedb/profile.yaml` selects `benchmark_1m` and
`vectorchord/profile.yaml` preserves the 42-probe connection default. The
`PARADEDB_URL`, `VECTORSEARCH_URL`, and `VECTORCHORD_URL` environment variables
override those defaults for loading. In k6, the legacy ParadeDB type passes
`__ENV.PARADEDB_URL` explicitly; new vector profile types resolve their environment
variables automatically.
The dataset Compose file creates `benchmark_1m` on ParadeDB on its first start.

```bash
# From the repository root; start the three PostgreSQL-based services.
docker compose -f datasets/cohere-1m/docker-compose.yml up -d --build --wait paradedb vectorsearch vectorchord
./bin/loader load --backend paradedb ./datasets/cohere-1m
./bin/loader load --backend vectorsearch --workers 4 --batch-size 2000 ./datasets/cohere-1m
./bin/loader load --backend vectorchord --workers 4 --batch-size 2000 ./datasets/cohere-1m
./k6 run --out dashboard datasets/cohere-1m/k6/postgres-profiles.js
```

The comparison runs the three strategies sequentially with separate aliases,
query metrics, and extension telemetry. Each backend block uses
`{ type: "<name>" }`. Set `DURATION` and `VUS` to adjust
the run. Set the corresponding `*_CONTAINER` variable to an empty string to
disable Docker metrics for a remote service.

`vectorsearch` is Timescale's **pg_vectorsearch / PRISM**, not pgvectorscale /
DiskANN. The Dockerfile builds the pinned revision from the source checkout.
Use `k6/vectorsearch.js` for a standalone run and
`k6/measure_recall_vectorsearch.py` to measure its recall. Its `VECTORSEARCH_URL`
can include session options such as `options=-c%20prism.nprobe%3D40`.
Remeasure recall for all strategies after rebuilding or changing index settings;
the comparison script checks query success and result counts, not recall parity.
