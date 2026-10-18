# Data Loader

The loader CLI handles bulk data loading with lifecycle scripts. It reads your
dataset's `schema.yaml` and its data sources (`data.parquet`, `data.csv`, or a
sharded Parquet `data/` directory; one source per table for multi-table
datasets), validates the schema for each backend, runs backend-specific `pre`
scripts, bulk inserts the data, then runs `post` scripts.

## Commands

```bash
# Load data into all running backends
./bin/loader load ./datasets/sample

# Load into a specific backend
./bin/loader load --backend paradedb ./datasets/sample

# Load with parallel workers
./bin/loader load --backend paradedb --workers 4 --batch-size 10000 ./datasets/sample

# Run pre scripts and load data, but skip post scripts (e.g. create an unindexed heap snapshot)
./bin/loader load --backend paradedb --pre-only ./datasets/sample

# Re-run only the post scripts against already-loaded data (e.g. rebuild indexes)
./bin/loader load --backend paradedb --post-only ./datasets/sample

# Drop all data
./bin/loader drop ./datasets/sample

# Pull dataset from S3
./bin/loader pull --dataset large --source s3://fts-bench/datasets/large/

# Pull from a public S3 bucket (no credentials needed)
./bin/loader pull --dataset test --source s3://fts-bench/datasets/test/ --anonymous

# Pull only configuration from a prefix, leaving its large compressed CSV in S3
./bin/loader pull --dataset test --source s3://fts-bench/datasets/test/ --exclude data.csv.gz
```

For prefix-based pulls, CSV objects ending in `.csv.gz` are decompressed while
they download and written without the `.gz` suffix. For example,
`data.csv.gz` becomes `data.csv`, while `data/posts.csv.gz` becomes
`data/posts.csv`. The compressed copy is not stored locally.

Use repeatable `--exclude <relative-path>` flags to omit objects from an S3
prefix pull. Exclusions are exact relative paths and are not supported when the
source itself is a `.tar.gz` or `.tgz` archive.

Build the loader with:

```bash
make loader
```

## Backend Selection

```bash
# Use ParadeDB's backend on the PostgreSQL engine
./bin/loader load --backend paradedb ./datasets/sample

# Override the selected backend connection
./bin/loader load --backend paradedb \
  --connection postgres://postgres:postgres@localhost:5432/benchmark ./datasets/sample

# Drop the selected backend's tables/indexes
./bin/loader drop --backend paradedb ./datasets/sample

# Load a custom strategy using an external manifest directory
./bin/loader load --backend custom --backends-dir ./my-backends ./datasets/sample
```

Connection precedence with backend selection is `--connection`, then the
manifest's `envVar`, then its `connection`. Drivers that support another endpoint
setting can accept an empty connection; Turbopuffer uses `TURBOPUFFER_REGION`.
A connection without a selected `--backend` is rejected.

`--pre-only`, `--post-only`, `--workers`, CSV, Parquet, and multi-table loading
work with explicit backends. The backend selects the dataset subdirectory;
its driver selects data conversion and drop behavior. See
[Backends](datasets.md#backends) for manifests and dataset overrides.

Omitting `--backend` preserves the historical default target list, including
ParadeDB; adding a backend does not silently add another load or drop target.
The `--backend` value selects the backend, and its manifest selects the driver.

## PostgreSQL Vector Backends

`paradedb`, `pg_vectorsearch` (Timescale pg_vectorsearch / PRISM), and `vectorchord`
all use the shared `postgres` driver. Their manifests supply extension-specific
capture and telemetry; their dataset directories supply index DDL.

```bash
docker compose --profile pg_vectorsearch --profile vectorchord up -d --build
./bin/loader load --backend pg_vectorsearch --workers 4 ./datasets/cohere-1m
./bin/loader load --backend vectorchord --workers 4 ./datasets/cohere-1m
```

The default ports are 5434 for pg_vectorsearch and 5435 for VectorChord.
`PG_VECTORSEARCH_URL` and `VECTORCHORD_URL` override manifest defaults. Select
these backends with `--backend`; both use PostgreSQL vector schema support.

For the three-backend comparison, dataset-specific defaults, recall measurement
scripts, and setup instructions, see the
[Cohere backend example](../datasets/cohere-1m/README.md#postgresql-backend-comparison).

## Connection Strings

The loader reads connection strings from environment variables:

```bash
export PARADEDB_URL="postgres://postgres:postgres@localhost:5432/benchmark"
export POSTGRES_URL="postgres://postgres:postgres@localhost:5433/benchmark"
export ELASTICSEARCH_URL="https://elastic:elastic@localhost:9200"
export OPENSEARCH_URL="http://localhost:9201"
export CLICKHOUSE_URL="clickhouse://default:clickhouse@localhost:9000/default"
export MONGODB_URL="mongodb://localhost:27017"
```

When using the Docker Compose profiles, the defaults work out of the box - no environment variables needed.

For S3 pulls, the AWS SDK uses its standard credential chain (environment variables, `~/.aws/credentials`, IAM role, etc.). You can set `AWS_REGION` and `AWS_PROFILE` to control which region and profile are used. Use `--anonymous` for public buckets.

## Data Source Support

The CLI imports one table/index/collection per `tables` entry (or the single
top-level `table`). It supports scalar CSV and Parquet columns for every
backend, including sharded Parquet directories. Single-table data lives at the
dataset root (`data.parquet`, `data.csv`, or `data/`); multi-table data lives
under `data/` as `<table>.parquet`, `<table>.csv`, or a `<table>/` shard
directory per table. `vector(n)` schema columns are supported for ParadeDB,
PostgreSQL, Elasticsearch, OpenSearch, and Turbopuffer; the CLI checks support
before running setup scripts.

The k6-side loader API is separate from this CLI. `db.loader().openDocuments()`
remains a CSV-only helper that returns strings. `loader.load()` also uses string
CSV values unless you supply `schema: "../schema.yaml"`; with a schema it uses
the CLI's typed reader for CSV, Parquet files, and Parquet shard directories.
`db.loader().openDocumentStream(path, {schema})` provides the same typed data for
insert/update workloads. Use typed loading for numeric IDs and vectors so
subsequent writes preserve their types. See [streaming workloads](scripting.md#streaming-csv-and-parquet-workloads).

## Integration Smoke Tests

Vector COPY/query behavior can be checked against live databases with:

```bash
BENCHMARKER_POSTGRES_VECTOR_URL="postgres://postgres:postgres@localhost:5433/benchmark" \
go test -tags integration ./drivers/shared/postgres

BENCHMARKER_PARADEDB_VECTOR_URL="postgres://postgres:postgres@localhost:5432/benchmark" \
go test -tags integration ./backends -run TestParadeDB
```

With `TURBOPUFFER_API_KEY` and `TURBOPUFFER_REGION` exported, the cloud lifecycle
test checks CSV/Parquet loading, inserts, updates, vector queries, and BM25 in
a temporary namespace that it deletes afterward:

```bash
BENCHMARKER_TURBOPUFFER_LIVE=1 \
go test -tags integration -run '^TestLiveTurbopufferLifecycle$' -count=1 -v ./drivers/turbopuffer
```

## Dataset Structure

See [Datasets](datasets.md) for full details on directory structure, schema format, and pre/post script formats.

### Compressed Extraction Limits

S3 `.tar.gz` and `.tgz` downloads are limited to 100 GiB of decompressed data by default, including tar metadata, skipped entries, and trailing data. Set `--max-extracted-bytes <positive-byte-count>` on `loader pull` to choose a different budget for your dataset.

The same limit applies separately to each `.csv.gz` object in a prefix-based
pull. Partially decompressed CSV files are removed if extraction fails or the
limit is exceeded.

Downloads cannot escape the destination through parent paths or symlinks, and duplicate file entries are rejected. New files use mode `0600` and new directories use `0750`.

## Turbopuffer (Cloud)

Turbopuffer uses the same loader and JSON query pattern as Elasticsearch, with
namespaces in place of indexes. It has no Docker Compose service or container
CPU/memory metrics. Query, ingest, update, and dashboard metrics work normally.

Set an API key and choose your [region](https://turbopuffer.com/docs/regions):

The commands below assume the dataset includes its Turbopuffer `pre.json`,
`post.json`, and k6 scripts. These files are distributed with datasets separately
from the backend code; download them with `loader pull` or create them using
the examples below.

```bash
export TURBOPUFFER_API_KEY="your-api-key"
export TURBOPUFFER_REGION="aws-us-east-1"
# Alternatively, set an explicit regional endpoint:
# export TURBOPUFFER_URL="https://aws-us-east-1.turbopuffer.com"

make loader k6
./bin/loader load --backend turbopuffer --batch-size 1000 ./datasets/sample
./k6 run --out dashboard datasets/sample/k6/turbopuffer.js
```

The sample requires its usual `data.csv` (see [Datasets](datasets.md)). Its
`pre.json` resets the cloud namespace `documents`, deleting any previous contents.
Change the dataset's table name, namespace in pre/post scripts, and query target
if you want to use a different namespace. All commands use your cloud account.

Connection precedence is an explicit k6 `connection`, then `TURBOPUFFER_URL`,
then the endpoint derived from `TURBOPUFFER_REGION`. The API key always comes
from `TURBOPUFFER_API_KEY`; keep it out of URLs and scripts. Both the CLI and k6
support these environment variables. HTTPS certificate verification is enabled.

Turbopuffer creates namespaces on the first document write. A setup object in
`turbopuffer/pre.json` configures subsequent writes on that driver:

```json
{
  "namespace": "documents",
  "reset": true,
  "schema": {
    "id": "uuid",
    "content": { "type": "string", "full_text_search": true }
  }
}
```

`reset` defaults to false. When true it deletes the namespace before loading;
a missing namespace is allowed. `schema` is attached to every batch, including
batches sent by parallel loader workers. See Turbopuffer's [write API](https://turbopuffer.com/docs/write)
for schema options. Each document must have an `id` or `_id`; `_id` is mapped
to Turbopuffer's `id`. IDs are preserved, so repeated IDs replace existing rows.
`insertBatch` and `updateBatch` both upsert complete documents, including vectors.
They replace the existing document, so include every attribute you want to retain.
Mixed batches preserve fields present in later documents; absent fields become null.
IDs must be non-empty strings of up to 64 bytes or non-negative integers. For
numeric IDs above JavaScript's safe-integer range, use typed data without converting
the ID to a JS Number, or use string IDs consistently across loading and queries.

For vector datasets, declare `vector(n)` in `schema.yaml` and the corresponding
Turbopuffer type in `pre.json`, for example:

```json
{
  "namespace": "documents",
  "distance_metric": "euclidean_squared",
  "schema": {
    "embedding": { "type": "[128]f32", "ann": true }
  }
}
```

Vectors retain their dataset column names. The default distance metric is
`cosine_distance`; set `distance_metric` in setup or use
`TURBOPUFFER_URL="https://aws-us-east-1.turbopuffer.com?distance_metric=euclidean_squared"`.
Use the same metric for every write to that namespace, including later k6
insert/update workloads. Load the namespace and its schema before running k6
insert/update workloads; those drivers use the stored schema and the distance
metric from their connection URL.

Typed float32 vectors with a configured vector schema (or named `vector`) use
Turbopuffer's lossless base64 wire format to reduce bulk-upload size. Ordinary
numeric arrays and vectors supplied as JavaScript arrays remain JSON arrays.

Arrays in pre/post files can contain setup objects for multiple namespaces and
HTTP operations, similar to Elasticsearch:

```json
[
  {
    "namespace": "documents",
    "wait_for_index": true,
    "timeout": "15m"
  },
  {
    "namespace": "documents",
    "method": "GET",
    "endpoint": "hint_cache_warm"
  }
]
```

Supported endpoints are the namespace root (omit `endpoint`), `query`,
`metadata`, and `hint_cache_warm`. `body` is sent as native JSON and the default
method is POST. An explicit DELETE at the namespace root deletes its data;
`loader drop --backend turbopuffer` does the same for each dataset table.
For an existing namespace, schema-only changes can be sent as a POST operation
with `body: {"schema": ...}`. The setup-object form only configures future writes.

`wait_for_index` polls namespace metadata until its index is up to date. Its
timeout defaults to 15 minutes and can be overridden as shown above. The sample
post script waits for indexing, then sends a [cache-warming hint](https://turbopuffer.com/docs/warm-cache).
The hint does not wait for warming to finish; include a warmup phase for warm
latency measurements. HTTP failures, including rate limits, are reported without
automatic retries so they remain visible in benchmark results.

For bulk imports, setup accepts `disable_backpressure: true`, as recommended in
Turbopuffer's [ingestion guide](https://turbopuffer.com/docs/ingestion). This allows
loading to continue when indexing falls behind. Use `wait_for_index` afterward
before benchmarking strongly consistent queries. Regular k6 writes default to
backpressure enabled. The Cohere 1M Turbopuffer scripts configure this bulk-load
flow and include a vector benchmark:

```bash
./bin/loader pull --dataset cohere-1m --source s3://paradedb-benchmarker/datasets/cohere-1m/ --anonymous
./bin/loader load --backend turbopuffer --workers 4 --batch-size 1000 ./datasets/cohere-1m
./k6 run datasets/cohere-1m/k6/turbopuffer.js
```

The Cohere setup resets `cohere_wiki`; use a separate namespace for independent
runs. Set `TURBOPUFFER_NAMESPACE` to target that namespace in the k6 script.
