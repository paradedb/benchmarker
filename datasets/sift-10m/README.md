# SIFT 10M — Vector Benchmark (unfiltered, L2)

Unfiltered top-10 kNN over 10M SIFT descriptors (128d, L2 distance):
ParadeDB (pg_search IVF vector index) vs Elasticsearch (`dense_vector` HNSW).
Each engine's container is capped at 12g / 6 CPUs. ParadeDB runs
`shared_buffers=8GB`: the IVF build needs a segment's ~5GB of vectors to fit
in shared buffers, so the Docker VM needs at least 16GB (e.g.
`colima start --memory 16`) — enough for one engine at a time at these
limits, which is why Elasticsearch is behind a compose profile.

ParadeDB table / Elasticsearch index `sift` with `_id` and `emb` (128d).

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

To rebuild only the index against the rows already in the volume (e.g. after
changing `post.sql` or the image), skip the table recreate and data load:

```bash
./bin/loader load --backend paradedb --post-only ./datasets/sift-10m
```

The compose file defaults to `paradedb/paradedb:v0.26.0-pg18`:
`vector_router` and `paradedb.vector_recall_target` don't exist before
0.26. To benchmark a pg_search build from source, build an image with
paradedb's `docker/Dockerfile.source` and override the default:

```bash
docker build -f docker/Dockerfile.source \
  --build-arg BASE_IMAGE=paradedb/paradedb:v0.26.0-pg18 \
  -t paradedb-source:main-<sha> .   # from a paradedb checkout
PARADEDB_IMAGE=paradedb-source:main-<sha> \
  docker compose -f datasets/sift-10m/docker-compose.yml up -d
```

`post.sql` builds the index with `vector_router = 'ivf'` and
`target_segment_count = 1`, then sets the recall knobs on the database:
`paradedb.vector_cluster_max_probe = 0.01` and
`paradedb.vector_recall_target = 0.95`.

Postgres parallelism follows the 6-CPU limit: `max_parallel_workers=6`, and
`max_parallel_workers_per_gather` / `max_parallel_maintenance_workers` are 5,
so each query or build is a leader plus 5 workers. If you change the CPU
limit in the compose file, change these with it.

The load takes ~63s (160k rows/s). With 8 segments and `shared_buffers=2GB`
the index build took ~471s (9.4GB index); the single-segment build takes
~26min on 6 CPUs (12GB index).

### Elasticsearch

```bash
docker compose -f datasets/sift-10m/docker-compose.yml stop paradedb
docker compose -f datasets/sift-10m/docker-compose.yml --profile elasticsearch up -d elasticsearch
./bin/loader load --backend elasticsearch --workers 4 --batch-size 5000 ./datasets/sift-10m
```

Elasticsearch 9.5.3 on the free Basic license, configured as close to
Elastic Cloud's Vector Database projects as Basic allows:

- `index.mode: vectordb_document`, which Vector Database projects force on
  every index. It excludes vectors from `_source`, preloads the vector files
  (`vex`, `veq`, `veb`, `cenivf`) into the page cache when the index opens,
  and runs merges in parallel without I/O throttling.
- `element_type: bfloat16` and `int8_hnsw` (m=16, ef_construction=100,
  `l2_norm`): the defaults this mode picks on Basic, pinned so the mapping
  doesn't change with the license.
- Cloud's default index type is `bbq_disk` (k-means partitions + binary
  quantization, the closest analog to ParadeDB's IVF router). It needs an
  Enterprise license: on Basic the mapping is accepted, but indexing fails
  with `current license is non-compliant for [bbq_disk]`. To benchmark it,
  start the 30-day trial first
  (`curl -X POST 'localhost:9200/_license/start_trial?acknowledge=true'`)
  and set `index_options.type` to `bbq_disk`.

Segments are left to Elasticsearch's background merges, with no force-merge.
Elastic's
[kNN tuning guide](https://www.elastic.co/docs/deploy-manage/production-guidance/optimize-performance/approximate-knn-search#reduce-the-number-of-index-segments)
recommends bulk loading with `refresh_interval: -1` "instead of force
merging", which `pre.json` does. `post.json` then restores the default
refresh interval and refreshes. Merges keep running after the loader exits,
so wait until `GET _cat/segments/sift?v` stops changing before benchmarking.
Heap is 4g of the 12g container limit; the
graph, int8 vectors and bfloat16 raw vectors are read off-heap through the
page cache. The preload runs when the index opens, so restarting the
container leaves it warm; drop caches after the restart to test it cold.

## Cache size

The compose settings (`shared_buffers=8GB`, 12g container) are what the
index build needs. Searching an already-built index doesn't, so the cache
can be shrunk below the ~12GB index + ~5GB heap without rebuilding. Lower
`shared_buffers`, `effective_cache_size` and the container memory limit
together in `docker-compose.yml` (the OS page cache is bounded only by the
container limit), e.g. `2GB` / `4GB` / `4g`, then recreate the container
and drop caches:

```bash
docker compose -f datasets/sift-10m/docker-compose.yml up -d paradedb
colima ssh -- sudo sh -c 'sync; echo 3 > /proc/sys/vm/drop_caches'
```

Check `buffer_reads` in `paradedb.vector_stats` output to confirm queries
actually miss. On macOS, Colima's disk image can sit in the host page cache,
so out-of-cache latencies are optimistic compared with a Linux host.

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
values for a single run.

Elasticsearch's recall knob is `num_candidates`, sent per query. Sweep it,
then set the matching `ES_NUM_CANDIDATES` for k6:

```bash
for n in 50 100 200 400; do
  python3 measure_recall.py collect --backend elasticsearch \
    --num-candidates $n --out results/es-$n.json
done
python3 measure_recall.py compare results/paradedb.json results/es-*.json
```

## Run

```bash
./k6 run --out dashboard=live,json,html datasets/sift-10m/k6/vector.js
```

`paradedb_knn` runs first, then `elasticsearch_knn` after a 2s gap, so both
containers must be up for the whole run. Set the Elasticsearch operating
point from the recall sweep with `-e ES_NUM_CANDIDATES=<n>`.

## Measured recall@10

First 1000 held-out queries, `max_probe = 0.01`, `recall_target = 0.95`,
v0.26.0-rc.4: **0.881** (worst query 0.30), ~75ms/query unbenchmarked
through psql. This is below the 0.95 target; not yet checked how much of
the gap is exact-distance ties in the integer-valued SIFT vectors.
