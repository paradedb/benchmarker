# SIFT 10M — Vector Benchmark (unfiltered, L2)

Unfiltered top-10 kNN over 10M SIFT descriptors (128d, L2 distance) on the
ParadeDB (pg_search) vector index, container capped at 12g / 6 CPUs with
`shared_buffers=8GB`: the IVF build needs a segment's ~5GB of vectors to fit
in shared buffers, so the Docker VM needs at least 16GB (e.g.
`colima start --memory 16`).

Table `sift` with columns `_id` and `emb vector(128)`.

## Getting the data

Source: `s3://paradedb-benchmarks/datasets/sift/`.

- `sampled/10m/parquet/sift/000{0..9}.parquet`: 10 shards of 1M rows
  (`_id`, `emb`).
- `queries/sift_queries.parquet`: 10k held-out query vectors (`id`, `emb`).
- `queries/ground_truth_knn_top10_unfiltered_10m.parquet`: exact L2 top-10
  per query (`query_id`, `gt_ids`).

The loader expects the corpus shards directly under `data/` and the queries
under `queries/`:

```bash
aws s3 sync s3://paradedb-benchmarks/datasets/sift/sampled/10m/parquet/sift/ datasets/sift-10m/data/
aws s3 sync s3://paradedb-benchmarks/datasets/sift/queries/ datasets/sift-10m/queries/
```

With a local copy already on disk, symlink instead of copying:

```bash
ln -s /path/to/sift10m-parquet/sampled/10m/parquet/sift datasets/sift-10m/data
ln -s /path/to/sift10m-parquet/queries datasets/sift-10m/queries
```

Then generate `k6/query_vectors.json` and `k6/ground_truth_top10.json`
(requires `pyarrow`; `measure_recall.py` is stdlib-only):

```bash
cd datasets/sift-10m/k6 && uvx --with pyarrow python make_query_vectors.py
```

## Load

Build the loader and k6 binaries first (`make build`), then:

```bash
docker compose -f datasets/sift-10m/docker-compose.yml up -d
./bin/loader load --backend paradedb --workers 4 --batch-size 5000 ./datasets/sift-10m
```

The compose file pins `paradedb/paradedb:v0.26.0-rc.4-pg18`: `vector_router`
and `paradedb.vector_recall_target` don't exist in 0.25.x.

`post.sql` builds the index with `vector_router = 'ivf'` and
`target_segment_count = 8`, then sets the recall knobs on the database:
`paradedb.vector_cluster_max_probe = 0.01` and
`paradedb.vector_recall_target = 0.95`.

With 8 segments and `shared_buffers=2GB` the load took ~63s (160k rows/s)
and the index build ~471s, producing a 9.4GB index.

## Recall

Latency is only meaningful at a known recall, and k6 never checks the
returned ids. `k6/measure_recall.py` runs the same query as `vector.js`,
records the ids each backend returns, and scores them against the exact
ground truth:

```bash
cd datasets/sift-10m/k6
python3 measure_recall.py collect --backend paradedb --limit 1000
python3 measure_recall.py compare results/paradedb.json
```

`collect` uses the settings `post.sql` put on the database; pass
`--set paradedb.vector_cluster_max_probe=0.02` (repeatable) to try other
values for a single run. `compare` accepts several results files, so
backends added later are scored side by side.

## Run

```bash
./k6 run --out dashboard=live,json,html datasets/sift-10m/k6/vector.js
```

## Measured recall@10

First 1000 held-out queries, `max_probe = 0.01`, `recall_target = 0.95`,
v0.26.0-rc.4: **0.881** (worst query 0.30), ~75ms/query unbenchmarked
through psql. This is below the 0.95 target; not yet checked how much of
the gap is exact-distance ties in the integer-valued SIFT vectors.
