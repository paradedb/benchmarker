# **Contributing**

Welcome! We're excited that you're interested in contributing and want to make the process as smooth as possible.

## Technical Info

Before submitting a pull request, please review this document, which outlines what conventions to follow when submitting changes. If you have any questions not covered in this document, please reach out to us in the [ParadeDB Community Slack](https://join.slack.com/t/paradedbcommunity/shared_invite/zt-32abtyjg4-yoYoi~RPh9MSW8tDbl0BQw) or via [email](mailto:support@paradedb.com).

### Selecting GitHub Issues

All external contributions should be associated with a GitHub issue. If there is no open issue for the bug or feature that you'd like to work on, please open one first. When selecting an issue to work on, we recommend focusing on issues labeled `good first issue`.

Ideal issues for external contributors include well-scoped, individual features (e.g. adding support for a new search backend or dataset) as those are less likely to conflict with our general development process.

### Prerequisites

- **Go 1.24.5+**
- **Docker & Docker Compose**
- **golangci-lint**

### Development Setup

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

### Project Structure

```text
benchmarks/
├── module.go              # k6 module registration
├── backends.go            # Backend config for k6
├── backends/
│   ├── driver.go          # Driver interface
│   ├── shared/
│   │   ├── postgres/      # Shared PostgreSQL driver
│   │   └── elastic/       # Shared Elasticsearch/OpenSearch driver
│   ├── paradedb/          # ParadeDB registration
│   ├── postgresfts/       # PostgreSQL FTS registration
│   ├── /      #  registration
│   │   └── docker/        #  Dockerfile
│   ├── elasticsearch/     # Elasticsearch
│   ├── opensearch/        # OpenSearch
│   ├── clickhouse/        # ClickHouse
│   └── mongodb/           # MongoDB
├── metrics/               # Metrics collection
├── dashboard/             # Real-time dashboard + SSE output
├── loader/                # k6 data loader module
├── cmd/
│   ├── loader/            # Loader CLI
│   └── dashboard-viewer/  # Dashboard replay CLI
├── datasets/              # Sample datasets + k6 scripts
└── docker-compose.yml     # Local backend setup
```

### Adding a Backend

1. Create `backends/<name>/driver.go` (or `register.go`) implementing the interface in `backends/driver.go`
2. Register it with `backends.Register(...)` and import the package for side-effect registration in `backends.go`
3. Add a Docker Compose service with a profile in `docker-compose.yml`
4. Add dataset config under `datasets/sample/<name>/` with `pre` and `post` scripts
5. Update the README with usage examples

### Adding a Dataset

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

SQL backends use `.sql` files, HTTP backends (Elasticsearch, OpenSearch, MongoDB) use `.json`.

### Pull Request Workflow

All changes to ParadeDB Benchmarks happen through GitHub Pull Requests. Here is the recommended flow for making a change:

1. Before working on a change, please check if there is already a GitHub issue open for it. If there is not, please open one first. This gives the community visibility into your work and allows others to make suggestions and leave comments.
2. Fork the repo and branch out from the `main` branch.
3. Make your changes. If you've added new functionality, please add tests.
4. Run `make fmt` and `make lint` to ensure code quality.
5. Run `make test` to verify nothing is broken.
6. Open a pull request towards the `main` branch with a clear description. Ensure that all tests and checks pass.
7. Congratulations! Our team will review your pull request.

### Reporting Issues

File issues at [GitHub Issues](https://github.com/paradedb/benchmarks/issues) with:

- A clear description of the problem or feature request
- Steps to reproduce (for bugs)
- Expected vs. actual behavior
- Environment details (OS, Go version, Docker version)

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
