# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

```bash
make              # Build k6 binary and loader CLI
make k6           # Build k6 with xk6-search extension (uses xk6)
make loader       # Build loader CLI to bin/
make viewer       # Build dashboard-viewer CLI to bin/
make test         # go test -v ./...
make fmt          # go fmt ./...
make lint         # golangci-lint run
make deps         # Install Go modules + xk6
make clean        # Remove k6 binary and bin/
```

Start backends with Docker Compose profiles:

```bash
docker compose --profile paradedb up -d         # Single backend
docker compose --profile all up -d              # All backends
```

Load data and run benchmarks:

```bash
./bin/loader load ./datasets/sample             # Load into all running backends
./bin/loader load --backend paradedb ./datasets/sample
./k6 run --out dashboard datasets/sample/k6/simple.js
```

## Architecture

This is a **k6 extension** (`xk6-search`) that provides a unified API for benchmarking full-text search across multiple database backends. It's built with xk6 and registers as `k6/x/search`.

### Module System

`module.go` registers the root k6 module. Each VU (Virtual User) gets a `ModuleInstance` that exports three functions to JavaScript:

- `backends(config)` → initializes backend drivers based on config map
- `metrics()` → creates a Docker container metrics collector
- `loader()` → creates a CSV document reader for k6 scripts

### Backend Plugin Architecture

Backends use a **registry pattern** with factory functions:

1. Each backend package calls `Register()` in its `init()` function (e.g., `backends/paradedb/register.go`, `backends/postgresfts/register.go`, and `backends//register.go`)
2. `backends.go:newBackends()` reads the JS config, looks up registered factories, creates drivers, and wraps each in a `K6Client`
3. `K6Client` (in `backends/driver.go`) is the adapter that wraps every `Driver` to add k6 metric emission (timing, tagging, sample pushing)

The `Driver` interface is minimal: `Close()`, `Exec()`, `Query()`, `Insert()`, `CaptureConfig()`. Adding a new backend means implementing this interface and calling `Register()`.

### Metrics Flow

`metrics/results.go` registers four k6 metrics (`search_duration`, `search_hits`, `ingest_duration`, `ingest_docs`), all tagged with `backend=<name>`. `K6Client.Search()` and `K6Client.InsertBatch()` time operations and push samples to k6's metric engine.

`metrics/collector.go` reads Docker container stats via `/var/run/docker.sock` and emits `container_cpu_percent` and `container_memory_bytes` gauges.

### Dashboard

`dashboard/output.go` implements k6's `output.Output` interface. It runs an HTTP server on `:5665` serving an embedded static UI (`dashboard/static/index.html`) and streams metrics via SSE (`/events`). It buffers k6 samples, aggregates them into timeline points with percentile breakdowns (P50/P90/P95/P99), and broadcasts every 200ms. Set `DASHBOARD_EXPORT=true` to save a JSON snapshot at test end.

### Loader

Two modes:

- **CLI** (`cmd/loader/main.go`): Reads `schema.yaml` + `data.csv`, runs backend-specific `pre.sql`/`pre.json`, bulk inserts, then runs `post.sql`/`post.json`. Supports `--batch-size`, `--workers`, and S3 pulls.
- **k6 module** (`loader/loader.go`): Opens CSV files with global caching, provides `Next()`, `NextBatch()`, `NextBatchNewIds()` with atomic counters for thread-safe VU pagination.

## Key Conventions

- Backend connection strings come from environment variables (`PARADEDB_URL`, `ELASTICSEARCH_URL`, etc.) or inline config in k6 scripts
- SQL backends (PostgreSQL variants, ClickHouse) use `.sql` lifecycle scripts; HTTP backends (Elasticsearch, OpenSearch, MongoDB) use `.json`
- Dataset directories follow a strict structure: `schema.yaml`, `data.csv`, per-backend subdirectories with `pre`/`post` scripts, and a `k6/` directory for benchmark scripts
- The shared postgres driver (`backends/shared/postgres/`) handles three separate backends (paradedb, postgresfts, ) via different registrations with different default ports and env vars
