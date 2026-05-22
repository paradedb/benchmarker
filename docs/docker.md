# Docker Setup

Docker is **optional**. You can run benchmarks against any database instance - local installs, cloud services, or remote servers. Without Docker, you lose container CPU/memory metrics in the dashboard, but everything else works.

## Per-dataset compose (recommended)

Each dataset under `datasets/` ships with its own `docker-compose.yml` that
pins the exact images and tuning used for that benchmark. This is the
recommended way to run a benchmark — the captured `Container` tab in the
dashboard will reflect the real images/configuration used:

```bash
docker compose -f datasets/sample/docker-compose.yml up -d
```

## Repo-root template (kitchen sink)

The repo-root `docker-compose.yml` is a kitchen-sink template containing every
supported backend behind compose profiles. It's intended as a starting point
when authoring a new dataset, not for running existing ones — copy the
relevant services into a new `datasets/<name>/docker-compose.yml`.

## Off-host backends

When benchmarking against managed services or remote hosts (e.g. AWS RDS,
managed Elasticsearch), there is no local container to inspect. Pass an empty
`container` in the JS backend config to skip docker capture for that backend:

```js
const backends = db.backends({
  backends: [{ type: "paradedb", container: "", connection: "postgres://..." }],
});
```

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

| Service       | Profile         | Port(s)    |
| ------------- | --------------- | ---------- |
| paradedb      | `paradedb`      | 5432       |
| postgres      | `postgres`      | 5433       |
| elasticsearch | `elasticsearch` | 9200       |
| opensearch    | `opensearch`    | 9201, 9600 |
| clickhouse    | `clickhouse`    | 9000, 8123 |
| mongodb       | `mongodb`       | 27017      |

## TLS

Elasticsearch runs with TLS enabled by default (self-signed certificates). The driver skips certificate verification automatically (`ELASTICSEARCH_SKIP_TLS_VERIFY` defaults to `true`). Set it to `false` if you're connecting to a cluster with valid certificates.

For OpenSearch, TLS is disabled in the Docker Compose config. If connecting to an external OpenSearch cluster with self-signed HTTPS, set `OPENSEARCH_SKIP_TLS_VERIFY=true`.
