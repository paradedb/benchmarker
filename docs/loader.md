# Data Loader

The loader CLI handles bulk data loading with lifecycle scripts. It reads your dataset's `schema.yaml` and `data.csv`, runs backend-specific `pre` scripts, bulk inserts the data, then runs `post` scripts.

## Commands

```bash
# Load data into all running backends
./bin/loader load ./datasets/sample

# Load into specific backends
./bin/loader load --backend paradedb --backend elasticsearch ./datasets/sample

# Load with parallel workers
./bin/loader load --backend paradedb --workers 4 --batch-size 10000 ./datasets/sample

# Drop all data
./bin/loader drop ./datasets/sample

# Pull dataset from S3
./bin/loader pull --dataset large --source s3://fts-bench/datasets/large/
```

Build the loader with:

```bash
make loader
```

## Connection Strings

The loader reads connection strings from environment variables:

```bash
export PARADEDB_URL="postgres://postgres:postgres@localhost:5432/benchmark"
export POSTGRES_FTS_URL="postgres://postgres:postgres@localhost:5433/benchmark"
export ELASTICSEARCH_URL="http://localhost:9200"
export OPENSEARCH_URL="http://localhost:9201"
export CLICKHOUSE_URL="clickhouse://default:clickhouse@localhost:9000/default"
export MONGODB_URL="mongodb://localhost:27017"
```

When using the Docker Compose profiles, the defaults work out of the box - no environment variables needed.

## Dataset Structure

See [Datasets](datasets.md) for full details on directory structure, schema format, and pre/post script formats.
