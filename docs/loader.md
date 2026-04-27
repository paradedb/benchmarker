# Data Loader

The loader CLI handles bulk data loading with lifecycle scripts. It reads your dataset's `schema.yaml` and `data.csv`, runs backend-specific `pre` scripts, bulk inserts the data, then runs `post` scripts.

## Commands

```bash
# Load data into all running backends
./bin/loader load ./datasets/sample

# Load into a specific backend
./bin/loader load --backend paradedb ./datasets/sample

# Load with parallel workers
./bin/loader load --backend paradedb --workers 4 --batch-size 10000 ./datasets/sample

# Drop all data
./bin/loader drop ./datasets/sample

# Pull dataset from S3
./bin/loader pull --dataset large --source s3://fts-bench/datasets/large/

# Pull from a public S3 bucket (no credentials needed)
./bin/loader pull --dataset test --source s3://fts-bench/datasets/test/ --anonymous
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
export ELASTICSEARCH_URL="https://elastic:elastic@localhost:9200"
export OPENSEARCH_URL="http://localhost:9201"
export CLICKHOUSE_URL="clickhouse://default:clickhouse@localhost:9000/default"
export MONGODB_URL="mongodb://localhost:27017"
```

When using the Docker Compose profiles, the defaults work out of the box - no environment variables needed.

For S3 pulls, the AWS SDK uses its standard credential chain (environment variables, `~/.aws/credentials`, IAM role, etc.). You can set `AWS_REGION` and `AWS_PROFILE` to control which region and profile are used. Use `--anonymous` for public buckets.

## Dataset Structure

See [Datasets](datasets.md) for full details on directory structure, schema format, and pre/post script formats.
