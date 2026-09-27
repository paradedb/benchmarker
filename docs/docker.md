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

Both drivers verify HTTPS certificates and hostnames using the system certificate store. For a private or self-signed CA, install that CA in the system trust store before connecting. TLS verification cannot be disabled; `OPENSEARCH_SKIP_TLS_VERIFY=true` now returns an error.

The bundled Docker Compose services use HTTP with TLS disabled for local benchmarking.
