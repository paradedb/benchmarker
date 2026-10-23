# Cohere Wikipedia 10M — Vector Benchmark

Vector search benchmark over 10M Cohere Wikipedia passage embeddings (1024d),
comparing the ParadeDB (pg_search) vector index against PostgreSQL + pgvector
HNSW and Elasticsearch `dense_vector` kNN. ~20GB of parquet source data, sized
to be larger than the configured database memory.

Uses the same dataset as the paradedb/paradedb CI vector benchmarks
(`benchmarks/datasets/cohere`): table `cohere_wiki` with columns `_id`, `url`,
`title`, `text`, and `emb vector(1024)`.

## Getting the data

With configuration already in the checkout:

```bash
AWS_PROFILE=ci ./bin/loader pull --dataset cohere-10m
```

The manifest points to
`s3://paradedb-benchmark/datasets/cohere/v1/sampled/10m/parquet/cohere_wiki/`
with corpus shards placed directly under `data/`. Query assets are not downloaded
by the manifest. Existing nonempty corpus directories are never overwritten.

To fetch configuration too, use an absent or empty destination:

```bash
AWS_PROFILE=ci ./bin/loader pull --dataset cohere-10m-copy \
  --source s3://paradedb-benchmark/benchmarker/cohere-10m/
```

Query inputs are only needed when regenerating JSON fixtures. Fetch them
separately if they are not already available:

```bash
mkdir -p datasets/cohere-10m/queries
AWS_PROFILE=ci aws s3 cp s3://paradedb-benchmark/datasets/cohere/v1/queries/cohere_queries.parquet \
  datasets/cohere-10m/queries/cohere_queries.parquet
AWS_PROFILE=ci aws s3 cp s3://paradedb-benchmark/datasets/cohere/v1/queries/ground_truth_knn_top10_unfiltered_10m.parquet \
  datasets/cohere-10m/queries/ground_truth_knn_top10_unfiltered_10m.parquet
```

Generate matching k6 vectors and recall JSON explicitly (requires `pyarrow`):

```bash
cd datasets/cohere-10m/k6
python3 make_query_vectors.py
```

The generator checks 1-based query IDs and 1024-dimensional vectors. The checked-in
`ground_truth_top10_10m.json` matches the canonical unfiltered ground truth.

## Running

1. Generate the query vector pool and ground truth (requires `pyarrow`):

   ```bash
   cd datasets/cohere-10m/k6 && python3 make_query_vectors.py
   ```

2. Start the backends and load:

   ```bash
   docker compose -f datasets/cohere-10m/docker-compose.yml up -d
   ./bin/loader load ./datasets/cohere-10m
   ```

3. Run the benchmark:

   ```bash
   ./k6 run --out dashboard=live,json,html datasets/cohere-10m/k6/vector.js
   ```

## Elasticsearch notes

- The index maps `emb` as `dense_vector` (cosine, float32 HNSW, m=16 /
  ef_construction=64 — mirroring the pgvector index) and **excludes `emb`
  from `_source`**: 10M × 1024 floats as stored JSON would add ~100GB of
  `_source` alone. The indexed vector values (~41GB) remain on disk.
- The schema's `_id` column becomes the ES document id, not a source field.
- `num_candidates` is the recall knob (`-e ES_NUM_CANDIDATES=...`). Default
  150 = the measured 95% recall@10 operating point on the 10m single-segment
  index. Measured curve (measure_recall.py, 100 held-out queries):
  10 → 0.579, 20 → 0.717, 40 → 0.835, 80 → 0.895, 150 → 0.951,
  300 → 0.966, 600 → 0.984, 1000 → 0.989.
- ES defaults to `int8_hnsw` quantization for dense_vector; this mapping pins
  full-precision `hnsw` for apples-to-apples recall with pgvector. Switch
  `index_options.type` to `int8_hnsw` to benchmark the quantized default.

## Methodology notes

- **Recall parity**: ANN latency is only comparable at matched recall. Ground
  truth for the query set lives alongside the data
  (`queries/ground_truth_knn_top10_unfiltered_10m.parquet`). Tune `hnsw.ef_search`
  (postgres), `paradedb.vector_cluster_max_probe` (paradedb) — set via
  `ALTER DATABASE` in each backend's `post.sql` — and `num_candidates`
  (elasticsearch, per query) until all reach the same measured recall@10
  (e.g. 95%), then benchmark those operating points. The paradedb/paradedb
  CI sweeps these knobs over 10–1000.
- **Query vectors** come from the held-out Cohere query set
  (`queries/cohere_queries.parquet`), not from the corpus.
- **Consistency**: Elasticsearch is near-real-time (documents are searchable
  only after refresh) with no transactional reads — a model suited to
  append-only workloads where absolute read correctness isn't required. The
  SQL backends are read-your-writes. `post.json` refreshes and force-merges
  before any queries run, so measured queries see identical corpora.
