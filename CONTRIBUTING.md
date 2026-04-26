# Contributing

Welcome! We're excited that you're interested in contributing and want to make the process as smooth as possible.

Before submitting a pull request, please review this document. If you have any questions, reach out to us in the [ParadeDB Community Slack](https://paradedb.com/slack) or via [email](mailto:support@paradedb.com).

## Selecting GitHub Issues

All external contributions should be associated with a GitHub issue. If there is no open issue for the bug or feature that you'd like to work on, please open one first. When selecting an issue to work on, we recommend focusing on issues labeled `good first issue`.

Ideal issues for external contributors include well-scoped, individual features (e.g. adding support for a new search backend or dataset) as those are less likely to conflict with our general development process.

## Development Setup

### Prerequisites

- **Go 1.24.5+**
- **Docker & Docker Compose**
- **golangci-lint**

### Getting Started

```bash
git clone https://github.com/paradedb/benchmarks.git
cd benchmarks
make deps    # Install Go modules + xk6
make         # Build k6 and loader
```

To verify your setup, start the backends and run a benchmark:

```bash
docker compose --profile all up -d
./bin/loader load ./datasets/sample
./k6 run --out dashboard datasets/sample/k6/simple.js
```

### Build Targets

```bash
make              # Build everything
make k6           # Build k6 with extension
make loader       # Build loader CLI
make viewer       # Build dashboard viewer
make test         # Run tests
make fmt          # Format code
make lint         # Run linter
make clean        # Remove build artifacts
```

## Architecture

### Project Structure

```text
benchmarks/
├── module.go              # k6 module registration
├── backends.go            # Backend config for k6 + side-effect imports
├── backends/
│   ├── driver.go          # Driver interface, registry, K6Client wrapper
│   ├── shared/
│   │   ├── postgres/      # Shared PostgreSQL driver (paradedb, postgresfts)
│   │   └── elastic/       # Shared Elasticsearch/OpenSearch driver
│   ├── paradedb/          # ParadeDB registration
│   ├── postgresfts/       # PostgreSQL FTS registration
│   ├── elasticsearch/     # Elasticsearch registration
│   ├── opensearch/        # OpenSearch registration
│   ├── clickhouse/        # ClickHouse driver
│   └── mongodb/           # MongoDB driver
├── metrics/               # k6 metrics + Docker container stats
├── dashboard/             # Real-time dashboard + SSE output
├── loader/                # k6 data loader module
├── cmd/
│   ├── loader/            # Loader CLI
│   └── dashboard-viewer/  # Dashboard replay CLI
├── datasets/              # Sample datasets + k6 scripts
└── docker-compose.yml     # Local backend setup
```

### How the Plugin System Works

Backends use a **registry pattern** with factory functions:

1. Each backend package calls `backends.Register()` in an `init()` function, providing a `BackendConfig` with a factory, file type, env var, default connection, and container name.
2. `backends.go` imports each backend package for side-effect registration (e.g., `_ "github.com/paradedb/benchmarks/backends/paradedb"`).
3. At runtime, `newBackends()` reads the JS config, looks up registered factories, creates drivers, and wraps each in a `K6Client`.
4. `K6Client` (in `backends/driver.go`) wraps every `Driver` to add k6 metric emission — timing, tagging, and sample pushing.

### Metrics Flow

`metrics/results.go` registers four k6 metrics (`search_duration`, `search_hits`, `ingest_duration`, `ingest_docs`), all tagged with `backend=<name>`. `K6Client.Search()` and `K6Client.InsertBatch()` time operations and push samples to k6's metric engine.

`metrics/collector.go` reads Docker container stats via `/var/run/docker.sock` and emits `container_cpu_percent` and `container_memory_bytes` gauges.

## Adding a Backend

This is the most common type of contribution. Here's a step-by-step guide.

### 1. The Driver Interface

Every backend implements this interface from `backends/driver.go`:

```go
type Driver interface {
    Close() error
    Exec(ctx context.Context, statements string) error
    Query(ctx context.Context, query string, args ...any) (hitCount int, err error)
    Insert(ctx context.Context, table string, cols []string, rows [][]any) (int, error)
    Update(ctx context.Context, table string, keyCols []string, cols []string, rows [][]any) (int, error)
    CaptureConfig(ctx context.Context, backendName string)
}
```

| Method          | Purpose |
| --------------- | ------- |
| `Close`         | Clean up connections |
| `Exec`          | Run setup/teardown statements (pre/post scripts) |
| `Query`         | Execute a search query and return the hit count |
| `Insert`        | Bulk insert rows, return count inserted |
| `Update`        | Bulk upsert rows by key columns, return count updated |
| `CaptureConfig` | Capture backend version/settings for the dashboard |

### 2. Use a Shared Driver or Write Your Own

If your backend is PostgreSQL-based, use the shared postgres driver:

```go
// backends/mybackend/register.go
package mybackend

import (
    "github.com/paradedb/benchmarks/backends"
    "github.com/paradedb/benchmarks/backends/shared/postgres"
)

func init() {
    backends.Register("mybackend", backends.BackendConfig{
        Factory:     New,
        FileType:    "sql",
        EnvVar:      "MYBACKEND_URL",
        DefaultConn: "postgres://postgres:postgres@localhost:5436/benchmark",
        Container:   "mybackend",
    })
}

func New(connString string) (backends.Driver, error) {
    return postgres.New(connString)
}
```

If your backend is Elasticsearch-compatible, use the shared elastic driver similarly.

For a completely new backend type, implement the `Driver` interface directly. See `backends/clickhouse/` or `backends/mongodb/` for examples of standalone drivers.

### 3. Register the Side-Effect Import

Add a blank import in `backends.go` so the `init()` function runs:

```go
import (
    // ...existing imports...
    _ "github.com/paradedb/benchmarks/backends/mybackend"
)
```

### 4. Add Docker Compose Service

Add a service with a profile in `docker-compose.yml`:

```yaml
mybackend:
  image: mybackend:latest
  ports:
    - "5436:5432"
  profiles:
    - mybackend
    - all
```

### 5. Add Dataset Scripts

Create pre/post scripts under each dataset directory:

```text
datasets/sample/mybackend/
├── pre.sql    # (or pre.json for HTTP backends)
└── post.sql   # (or post.json)
```

- SQL backends (`.sql`): statements executed directly via `Exec()`
- HTTP backends (`.json`): structured JSON processed by the driver's `Exec()` method

### 6. Update Documentation

- Add the backend to the "Available Backend Types" table in `README.md`
- Add a query example under "Backend Examples" in `README.md`
- Add the Docker Compose service/port to the Docker table in `README.md`

### BackendConfig Fields

| Field         | Description |
| ------------- | ----------- |
| `Factory`     | `func(connString string) (Driver, error)` — creates a driver instance |
| `FileType`    | `"sql"` or `"json"` — determines pre/post script file extension |
| `EnvVar`      | Environment variable name for the connection string |
| `DefaultConn` | Fallback connection string when env var is unset |
| `Container`   | Docker container name for metrics collection |

## Datasets

New datasets are not accepted into the repository — the repo ships with small sample datasets only. To share your own datasets, publish them to S3 and users can pull them with:

```bash
./bin/loader pull --dataset <name> --source s3://<bucket>/<prefix>/
```

Datasets follow this structure:

```text
datasets/<name>/
├── schema.yaml           # Column definitions
├── data.csv              # Source data
├── paradedb/
│   ├── pre.sql           # Create tables
│   └── post.sql          # Create indexes, VACUUM
├── elasticsearch/
│   ├── pre.json          # Index mapping
│   └── post.json         # Refresh, force merge
└── k6/
    └── benchmark.js      # k6 test script
```

SQL backends use `.sql` files, HTTP backends (Elasticsearch, OpenSearch, MongoDB) use `.json`. You only need to add directories for the backends you want to test — the loader skips backends with no scripts.

## Pull Request Workflow

1. Before working on a change, check if there is already a GitHub issue open for it. If not, open one first.
2. Fork the repo and branch out from the `main` branch.
3. Make your changes. If you've added new functionality, please add tests.
4. Run `make fmt` and `make lint` to ensure code quality.
5. Run `make test` to verify nothing is broken.
6. Open a pull request towards the `main` branch with a clear description. Ensure that all tests and checks pass.
7. Our team will review your pull request.

## Reporting Issues

File issues at [GitHub Issues](https://github.com/paradedb/benchmarks/issues) with:

- A clear description of the problem or feature request
- Steps to reproduce (for bugs)
- Expected vs. actual behavior
- Environment details (OS, Go version, Docker version)

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
