# Cohere Wikipedia 10M — Vector Benchmark

Vector search benchmark over 10M Cohere Wikipedia passage embeddings (1024d),
comparing the ParadeDB (pg_search) vector index against PostgreSQL + pgvector
HNSW and Elasticsearch `dense_vector` kNN. ~20GB of parquet source data, sized
to be larger than the configured database memory.

Uses the same dataset as the paradedb/paradedb CI vector benchmarks
(`benchmarks/datasets/cohere`): table `cohere_wiki` with columns `_id`, `url`,
`title`, `text`, and `emb vector(1024)`.

## Getting the data

The assembled artifact (this config plus `data/` parquet shards, `queries/`,
and a pre-generated `k6/query_vectors.json`) lives at
`s3://paradedb-benchmarker/datasets/cohere-10m/`. On a fresh machine, one
clean pull gives a ready-to-load dataset:

```bash
./bin/loader pull --dataset cohere-10m --source s3://paradedb-benchmarker/datasets/cohere-10m/
```

Into an existing checkout (config already present), sync just the data:

```bash
aws s3 sync s3://paradedb-benchmarker/datasets/cohere-10m/data/ datasets/cohere-10m/data/
aws s3 sync s3://paradedb-benchmarker/datasets/cohere-10m/queries/ datasets/cohere-10m/queries/
aws s3 cp s3://paradedb-benchmarker/datasets/cohere-10m/k6/query_vectors.json datasets/cohere-10m/k6/
```

The upstream source is `s3://paradedb-benchmarks/datasets/cohere/` (100
parquet shards of ~100k rows plus the held-out query set).

## Running

1. Query vector pool: `k6/query_vectors.json` ships in the S3 artifact. To
   regenerate it from the queries parquet (requires `pyarrow`):

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
- `num_candidates` is the recall knob (`-e ES_NUM_CANDIDATES=...`, default
  40 to match the pg-side `hnsw.ef_search` starting point).
- ES defaults to `int8_hnsw` quantization for dense_vector; this mapping pins
  full-precision `hnsw` for apples-to-apples recall with pgvector. Switch
  `index_options.type` to `int8_hnsw` to benchmark the quantized default.

## Methodology notes

- **Recall parity**: ANN latency is only comparable at matched recall. Ground
  truth for the query set lives alongside the data
  (`queries/ground_truth_10m.parquet` and friends). Tune `hnsw.ef_search`
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
