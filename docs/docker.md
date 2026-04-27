# Docker Setup

Docker is **optional**. You can run benchmarks against any database instance - local installs, cloud services, or remote servers. Without Docker, you lose container CPU/memory metrics in the dashboard, but everything else works.

## Profiles

Start backends individually or all at once using Docker Compose profiles:

```bash
# Start the backends you need
docker compose --profile paradedb up -d

# Or multiple backends
docker compose --profile paradedb --profile elasticsearch up -d

# Or all of them
docker compose --profile all up -d
```

## Services

| Service       | Profile         | Port      |
| ------------- | --------------- | --------- |
| paradedb      | `paradedb`      | 5432      |
| postgresfts   | `postgresfts`   | 5433      |
| elasticsearch | `elasticsearch` | 9200      |
| opensearch    | `opensearch`    | 9201      |
| clickhouse    | `clickhouse`    | 9000/8123 |
| mongodb       | `mongodb`       | 27017     |

## TLS

For self-signed HTTPS OpenSearch clusters, set `OPENSEARCH_SKIP_TLS_VERIFY=true`.
