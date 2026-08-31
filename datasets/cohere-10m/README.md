# Cohere Wikipedia 10M — Vector Benchmark

Vector search benchmark over 10M Cohere Wikipedia passage embeddings (1024d),
comparing the ParadeDB (pg_search) vector index against PostgreSQL + pgvector
HNSW. ~20GB of parquet source data, sized to be larger than the configured
database memory.

Uses the same dataset as the paradedb/paradedb CI vector benchmarks
(`benchmarks/datasets/cohere`): table `cohere_wiki` with columns `_id`, `url`,
`title`, `text`, and `emb vector(1024)`.

## Getting the data

The corpus is sharded parquet (100 files, ~100k rows each) plus a query set:

```bash
aws s3 sync s3://paradedb-benchmarks/datasets/cohere/sampled/10m/parquet/cohere_wiki/ datasets/cohere-10m/data/
aws s3 sync s3://paradedb-benchmarks/datasets/cohere/queries/ datasets/cohere-10m/queries/
```

(`loader pull` requires an empty destination, so use `aws s3 sync` here — the
config lives in this directory.)

## Running

1. Generate the query vector pool (requires `pyarrow`):

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
   ./k6 run --out dashboard datasets/cohere-10m/k6/vector.js
   ```

## Methodology notes

- **Recall parity**: ANN latency is only comparable at matched recall. Ground
  truth for the query set lives alongside the data
  (`queries/ground_truth_10m.parquet` and friends). Tune `hnsw.ef_search`
  (postgres) and `paradedb.vector_cluster_max_probe` (paradedb) — set via
  `ALTER DATABASE` in each backend's `post.sql` — until both reach the same
  measured recall@10 (e.g. 95%), then benchmark those operating points. The
  paradedb/paradedb CI sweeps these knobs over 10–1000.
- **Query vectors** come from the held-out Cohere query set
  (`queries/cohere_queries.parquet`), not from the corpus.
