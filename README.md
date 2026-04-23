# ParadeDB Benchmarks

A k6 extension for benchmarking databases with a unified API, real-time dashboard, and comprehensive data loading tools. While the included datasets focus on full-text search, the framework works for any query workload — you write the SQL or API calls, it handles timing, metrics, and visualization.

Compare performance across **ParadeDB**, **PostgreSQL FTS**, ****, **Elasticsearch**, **OpenSearch**, **ClickHouse**, and **MongoDB Atlas Search** with consistent metrics and visualization. Docker Compose profiles are included for single-node benchmarking, but you can point at any database — local installs, remote servers, or cloud services. The k6 binary is customised to avoid disk I/O during runs so it doesn't interfere with benchmark measurements on the same machine.

## How It Works

You write a k6 script that defines which backends to test, what queries to run, and how many concurrent users. The framework handles everything else:

1. **Phases** — backends run in sequential phases so they don't compete for system resources during measurement. A timer manages the staggering automatically.
2. **Metrics** — every query and insert is timed and tagged with the backend name. Hit counts, latencies, and throughput are pushed to k6's metric engine.
3. **Container monitoring** — Docker CPU and memory usage is polled in the background and correlated with query performance.
4. **Dashboard** — results stream in real-time to a browser-based dashboard showing latency percentiles (P50/P90/P95/P99), QPS, ingest rate, and container resources per backend.
5. **Reproducibility** — the dashboard captures backend configs, pre/post scripts, and query patterns so results can be understood and reproduced later. Export as standalone HTML to share.

The typical workflow: start databases (Docker or remote), load a dataset with the loader CLI, run a k6 script, and view the dashboard.

## Quick Start

### 1. Build

```bash
make        # Builds both k6 and loader
```

Or individually:

```bash
make k6     # Build k6 with the extension
make loader # Build the loader CLI
```

### 2. Start the backends

```bash
# Start the backends you need
docker compose --profile paradedb up -d

# Or multiple backends
docker compose --profile paradedb --profile elasticsearch up -d

# Or all of them
docker compose --profile all up -d
```

### 3. Load test data

```bash
# Loads into all running backends
./bin/loader load ./datasets/sample

# Or limit to specific backends
./bin/loader load --backend paradedb --backend elasticsearch ./datasets/sample
```

### 4. Run a benchmark

```bash
./k6 run --out dashboard datasets/sample/k6/simple.js
```

Open http://localhost:5665/static/ to see real-time results.

## Datasets

A dataset is a self-contained directory with everything needed to load data and run benchmarks against one or more backends:

```text
datasets/sample/
├── schema.yaml              # Column names and types
├── data.csv                 # Source data
├── paradedb/
│   ├── pre.sql              # Create tables, set up schema
│   └── post.sql             # Create indexes, VACUUM ANALYZE
├── elasticsearch/
│   ├── pre.json             # Create index with mappings
│   └── post.json            # Refresh, force merge
├── clickhouse/
│   ├── pre.sql
│   └── post.sql
└── k6/
    ├── simple.js            # Benchmark scripts
    └── search_terms.json    # Query terms for benchmarks
```

Each backend subdirectory contains pre/post scripts that the loader runs before and after data loading. SQL backends (ParadeDB, PostgreSQL, ClickHouse) use `.sql` files, HTTP backends (Elasticsearch, OpenSearch, MongoDB) use `.json`. You only need directories for the backends you're testing.

The `k6/` directory holds your benchmark scripts and any supporting data like search terms. See the [Data Loader](#data-loader) section for details on schema, pre/post script formats, and loader CLI usage.

## Writing k6 Scripts

Scripts are standard [k6](https://grafana.com/docs/k6/latest/) JavaScript with the `k6/x/database` extension imported. k6 concepts like scenarios, executors, and VUs (virtual users, concurrent goroutines each running your test function in a loop) all apply. This extension adds backend drivers, a phase timer, and Docker metrics on top. You may find the [k6 scenarios documentation](https://grafana.com/docs/k6/latest/using-k6/scenarios/) useful as a reference.

### Basic Script Structure

```javascript
import db from "k6/x/database";

// 1. Configure backends
const backends = db.backends({
  backends: ["paradedb", "elasticsearch"],
});

// 2. Load search terms
const terms = db.terms(open("./search_terms.json"));

// 3. Define test scenarios with timer for sequential phases
const timer = db.timer({ duration: "30s", gap: "5s" });

const scenarios = {
  search_test: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.get(),
    exec: "searchQuery",
  },
};

// 4. Add Docker metrics collector and export — one line does both
export const collectMetrics = backends.addDockerMetricsCollector(scenarios, timer);

export const options = { scenarios };

// 5. Execute search queries
export function searchQuery() {
  const term = terms.next();
  backends
    .get("paradedb")
    .search(
      `SELECT id, title FROM documents WHERE content ||| $1 LIMIT 10`,
      term,
    );
}
```

### Module API

The `db` module (`k6/x/database`) provides:

| Function | Returns | Description |
| --- | --- | --- |
| `db.backends(config)` | `Backends` | Initializes backend drivers and Docker metrics collector from config |
| `db.timer({ duration, gap })` | `Timer` | Creates a phase timer for staggering scenarios |
| `db.loader()` | `Loader` | Creates a CSV document reader for ingest or update benchmarks |
| `db.terms(data)` | `Terms` | Loads a JSON array of query strings to avoid caching bias. `terms.next()` cycles sequentially, `terms.random()` picks randomly. Accepts a JSON string via `open()` or a k6 `SharedArray`. |

### Backend Configuration

Each backend in the `backends` array can be a string (uses defaults) or an object (full config):

```javascript
const backends = db.backends({
  backends: [
    "paradedb",  // String shorthand - uses defaults
    {
      type: "paradedb",           // Required: backend type
      alias: "paradedb-v2",       // Display name (defaults to type)
      connection: "postgres://localhost:5433/benchmark",
      container: "paradedb-v2",   // Docker container for metrics
      color: "#ff6b6b",           // Dashboard chart color
    },
    "elasticsearch",
  ],
});

// Access backends by alias
backends.get("paradedb").search(...);
backends.get("paradedb-v2").search(...);
backends.get("elasticsearch").search(...);
```

### Available Backend Types

The framework is database-agnostic — the current backends and datasets are focused on full-text search, but the same infrastructure works for any query workload. See [CONTRIBUTING.md](CONTRIBUTING.md) for how to add a new backend.

**PostgreSQL-based** (shared driver, different extensions):

| Type            | Description                              |
| --------------- | ---------------------------------------- |
| `paradedb`      | ParadeDB with pg_search (BM25)           |
| `postgresfts`   | PostgreSQL native FTS (tsvector/tsquery) |
| ``  | PostgreSQL with  extension  |

**Elasticsearch-based** (shared driver):

| Type            | Description |
| --------------- | ----------- |
| `elasticsearch` | Elasticsearch |
| `opensearch`    | OpenSearch    |

**Other**:

| Type            | Description               |
| --------------- | ------------------------- |
| `clickhouse`    | ClickHouse                |
| `mongodb`       | MongoDB with Atlas Search |

### Benchmark Patterns

[Scenarios](https://grafana.com/docs/k6/latest/using-k6/scenarios/) control how your test runs. Every benchmark needs at least one search/ingest scenario. Use `backends.addDockerMetricsCollector()` to add Docker CPU/memory monitoring — it adds the scenario and returns the collect function in one call.

#### Executors

k6 provides several executors that determine how virtual users are scheduled:

| Executor                 | Description                                                    | Best for                             |
| ------------------------ | -------------------------------------------------------------- | ------------------------------------ |
| `constant-vus`           | Fixed number of VUs running for a set duration                 | Most search benchmarks               |
| `constant-arrival-rate`  | Fixed iteration rate regardless of response time               | Rate-limited ingest, SLA testing     |
| `ramping-vus`            | VUs increase/decrease over stages                              | Load ramp-up, finding breaking point |
| `ramping-arrival-rate`   | Iteration rate increases/decreases over stages                 | Gradual load increase testing        |

k6 provides [additional executors](https://grafana.com/docs/k6/latest/using-k6/scenarios/executors/) for other use cases.

Each scenario specifies:

| Option            | Description                                                   |
| ----------------- | ------------------------------------------------------------- |
| `executor`        | How VUs are scheduled (see table above)                       |
| `vus`             | Number of virtual users (for VU-based executors)              |
| `duration`        | How long the scenario runs                                    |
| `startTime`       | When to start (for sequential phases)                         |
| `exec`            | Which exported function to run                                |
| `tags`            | Custom tags for grouping metrics in charts                    |
| `env`             | Per-scenario environment variables (readable via `__ENV`)     |
| `rate`            | Iterations per `timeUnit` (arrival-rate executors)            |
| `timeUnit`        | Time unit for `rate` (e.g., `"1s"`)                           |
| `preAllocatedVUs` | VUs pre-allocated for arrival-rate executors                  |

#### Pattern 1: Single Backend

The simplest benchmark — one backend, constant load. No timer needed:

```javascript
const scenarios = {
  pdb_search: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    exec: "pdbSearch",
  },
};
export const collectMetrics = backends.addDockerMetricsCollector(scenarios, "35s");

export const options = { scenarios };

export function pdbSearch() {
  backends.get("paradedb").search(
    `SELECT id, title FROM documents WHERE content ||| $1 LIMIT 10`,
    terms.next(),
  );
}
```

#### Pattern 2: Sequential Multi-Backend Comparison

Use `db.timer()` to stagger backends so they don't compete for system resources. `advanceAndGet()` returns a startTime string and advances; `get()` returns the same phase (for parallel scenarios):

```javascript
const timer = db.timer({ duration: "30s", gap: "5s" });

const scenarios = {
  // get() returns the current phase's startTime ("0s")
  pdb_search: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.get(),
    exec: "pdbSearch",
  },
  // get() again — parallel scenario in the same phase
  pdb_count: {
    executor: "constant-vus",
    vus: 3,
    duration: "30s",
    startTime: timer.get(),
    exec: "pdbCount",
  },
  // advanceAndGet() moves to the next phase ("35s")
  es_search: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.advanceAndGet(),
    exec: "esSearch",
  },
  es_count: {
    executor: "constant-vus",
    vus: 3,
    duration: "30s",
    startTime: timer.get(),
    exec: "esCount",
  },
};

// Adds a metrics_collector scenario and returns the collect function
export const collectMetrics = backends.addDockerMetricsCollector(scenarios, timer);

export const options = { scenarios };
```

- `get()` — returns the current phase's startTime (auto-advances on first call); use for the first scenario and for parallel scenarios in the same phase
- `advanceAndGet()` / `next()` — advances to the next phase and returns its startTime; use when starting a new phase
- `backends.addDockerMetricsCollector(scenarios, timer)` — adds a `metrics_collector` scenario and returns the collect function
- Also accepts a duration string: `backends.addDockerMetricsCollector(scenarios, "500s")`

#### Pattern 3: Parallel Search + Ingest

Run search queries and ingest in parallel within each phase. Use `env` to share exec functions across backends, and `constant-arrival-rate` for ingest to maintain a predictable insertion rate:

```javascript
import db from "k6/x/database";

const backends = db.backends({ backends: ["paradedb", "elasticsearch"] });
const terms = db.terms(open("./search_terms.json"));
const loader = db.loader();
const docs = loader.openDocuments("../data.csv");
const BATCH_SIZE = 1000;

const timer = db.timer({ duration: "30s", gap: "5s" });

const scenarios = {
  // ParadeDB: search + ingest in parallel
  pdb_search: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.get(),
    exec: "search",
    env: { BACKEND: "paradedb" },
  },
  pdb_ingest: {
    executor: "constant-arrival-rate",
    rate: 1,
    timeUnit: "1s",
    duration: "30s",
    startTime: timer.get(),
    preAllocatedVUs: 2,
    exec: "ingest",
    env: { BACKEND: "paradedb" },
  },
  // Elasticsearch: search + ingest in parallel
  es_search: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.advanceAndGet(),
    exec: "search",
    env: { BACKEND: "elasticsearch" },
  },
  es_ingest: {
    executor: "constant-arrival-rate",
    rate: 1,
    timeUnit: "1s",
    duration: "30s",
    startTime: timer.get(),
    preAllocatedVUs: 2,
    exec: "ingest",
    env: { BACKEND: "elasticsearch" },
  },
};
export const collectMetrics = backends.addDockerMetricsCollector(scenarios, timer);

export const options = { scenarios };

export function search() {
  const backend = __ENV.BACKEND;
  backends.get(backend).search(
    `SELECT id, title FROM documents WHERE content ||| $1 LIMIT 10`,
    terms.next(),
  );
}

export function ingest() {
  const backend = __ENV.BACKEND;
  const batch = docs.nextBatch(BATCH_SIZE, backend);
  backends.get(backend).insertBatch("documents", batch);
}
```

#### Pattern 4: Query Shape Comparison with Chart Tags

Compare different query types across backends. Use `tags: { chart: "name" }` to group scenarios into separate dashboard charts:

```javascript
const timer = db.timer({ duration: "30s", gap: "2s" });

const scenarios = {
  // Single term TopK — grouped on one chart
  pdb_single_term: {
    executor: "constant-vus",
    vus: 1,
    duration: "30s",
    startTime: timer.get(),
    exec: "pdbSingleTerm",
    tags: { chart: "single_term_topk" },
  },
  es_single_term: {
    executor: "constant-vus",
    vus: 1,
    duration: "30s",
    startTime: timer.advanceAndGet(),
    exec: "esSingleTerm",
    tags: { chart: "single_term_topk" },
  },
  // Count queries — grouped on a separate chart
  pdb_count: {
    executor: "constant-vus",
    vus: 1,
    duration: "30s",
    startTime: timer.advanceAndGet(),
    exec: "pdbCount",
    tags: { chart: "count" },
  },
  es_count: {
    executor: "constant-vus",
    vus: 1,
    duration: "30s",
    startTime: timer.advanceAndGet(),
    exec: "esCount",
    tags: { chart: "count" },
  },
};
export const collectMetrics = backends.addDockerMetricsCollector(scenarios, timer);

export const options = { scenarios };
```

Scenarios with the same `chart` tag appear on the same dashboard chart. Scenarios without a `chart` tag all share the default chart.

#### Pattern 5: Ramping Load

Gradually increase load to find the breaking point or measure behavior under varying concurrency:

```javascript
const scenarios = {
  ramp_test: {
    executor: "ramping-vus",
    startVUs: 1,
    stages: [
      { duration: "30s", target: 10 },   // Ramp up to 10 VUs
      { duration: "60s", target: 10 },   // Hold at 10
      { duration: "30s", target: 50 },   // Ramp up to 50 VUs
      { duration: "60s", target: 50 },   // Hold at 50
      { duration: "30s", target: 0 },    // Ramp down
    ],
    exec: "searchQuery",
  },
};
export const collectMetrics = backends.addDockerMetricsCollector(scenarios, "220s");

export const options = { scenarios };
```

### Ingest Benchmarks

To benchmark ingest performance, use the loader to open a document file and insert batches. Use scenario `env` to pass the backend name so one function handles all backends. The second argument to `nextBatch()` is a pool key: each pool has its own atomic counter, so VUs within a backend get non-overlapping batches, while different backends independently walk through the same data from the start.

See [Pattern 3](#pattern-3-parallel-search--ingest) for a complete example combining search and ingest.

### Update Benchmarks

```javascript
export function updateTest() {
  const backend = __ENV.BACKEND;
  const batch = docs.nextBatchSwapped(BATCH_SIZE, "content", backend);
  backends.get(backend).updateBatch("documents", batch);
}
```

The `nextBatchSwapped()` method lazily builds a copy of all documents with adjacent values of the given field swapped, then paginates through them atomically. The optional third argument is a pool key, same as `nextBatch()`.

### Backend Query Reference

#### ParadeDB (BM25)

```javascript
backends.get("paradedb").search(
  `SELECT id, title, pdb.score(id) as score
   FROM documents
   WHERE content ||| $1
   ORDER BY score DESC
   LIMIT 10`,
  "search term",
);
```

#### PostgreSQL FTS

```javascript
backends.get("postgresfts").search(
  `SELECT id, title, ts_rank(tsv, plainto_tsquery('english', $1)) as score
   FROM documents
   WHERE tsv @@ plainto_tsquery('english', $1)
   ORDER BY score DESC
   LIMIT 10`,
  "search term",
);
```

#### Elasticsearch / OpenSearch

```javascript
backends.get("elasticsearch").search("documents", {
  query: { match: { content: "search term" } },
  size: 10,
});
```

#### ClickHouse

```javascript
backends.get("clickhouse").search(
  `SELECT id, title
   FROM documents
   WHERE hasToken(content, 'term')
   LIMIT 10`,
);
```

#### MongoDB Atlas Search

```javascript
backends.get("mongodb").search(
  JSON.stringify({
    text: { query: "search term", path: ["content"] },
  }),
  "documents",
);
```

### Error Handling

The API validates configuration and fails fast with clear error messages:

```javascript
// Unknown backend type
backends: ["unknown"];
// → panic: backends: unknown backend type 'unknown'. Valid types: [paradedb elasticsearch ...]

// Missing backends array
db.backends({ paradedb: true });
// → panic: backends: 'backends' array is required

// Missing type in object config
backends: [{ alias: "test" }];
// → panic: backends: each backend config must have a 'type' field

// Duplicate alias
backends: ["paradedb", { type: "paradedb" }];
// → panic: backends: duplicate alias 'paradedb'
```

## Dashboard

The real-time dashboard shows:

- **Latency over time** - P50/P90/P95/P99 percentiles per backend
- **Query throughput** - Queries per second
- **Ingest rate** - Documents inserted per second
- **Container resources** - CPU and memory usage from Docker
- **Backend configuration** - Database settings and version info
- **Pre/Post scripts** - Full SQL/JSON scripts used for setup (for reproducibility)
- **Query patterns** - Actual queries executed per scenario

Enable with the `--out dashboard` flag:

```bash
./k6 run --out dashboard script.js
```

Then open http://localhost:5665/static/ in your browser.

### Export & Replay

```bash
# Export dashboard data during run
DASHBOARD_EXPORT=true ./k6 run --out dashboard benchmark.js

# View saved data later (use generated dashboard_<timestamp>.json file)
./bin/dashboard-viewer ./dashboard_2026-02-28_12-00-00.json

# Save as a standalone HTML file
./bin/dashboard-viewer --html report.html ./dashboard_2026-02-28_12-00-00.json
```

## Metrics

The extension emits standard k6 metrics with backend tags:

| Metric                   | Type    | Description                    |
| ------------------------ | ------- | ------------------------------ |
| `search_duration`        | Trend   | Search latency in milliseconds |
| `search_hits`            | Gauge   | Number of results returned     |
| `ingest_duration`        | Trend   | Insert latency in milliseconds |
| `ingest_docs`            | Counter | Documents inserted             |
| `container_cpu_percent`  | Gauge   | Container CPU usage percentage |
| `container_memory_bytes` | Gauge   | Container memory usage         |

## Data Loader

The loader CLI handles bulk data loading with lifecycle scripts.

### Commands

```bash
# Load data into all backends
./bin/loader load ./datasets/sample

# Load into specific backend
./bin/loader load --backend paradedb ./datasets/sample

# Load with parallel workers
./bin/loader load --backend paradedb --workers 4 --batch-size 10000 ./datasets/sample

# Drop all data
./bin/loader drop ./datasets/sample

# Pull dataset from S3
./bin/loader pull --dataset large --source s3://fts-bench/datasets/large/
```

### Loader Connection Strings

The loader CLI reads connection strings from environment variables:

```bash
export PARADEDB_URL="postgres://postgres:postgres@localhost:5432/benchmark"
export POSTGRES_FTS_URL="postgres://postgres:postgres@localhost:5433/benchmark"
export _URL="postgres://postgres:postgres@localhost:5435/benchmark"
export ELASTICSEARCH_URL="http://localhost:9200"
export OPENSEARCH_URL="http://localhost:9201"
export CLICKHOUSE_URL="clickhouse://default:clickhouse@localhost:9000/default"
export MONGODB_URL="mongodb://localhost:27017"
```

### Dataset Structure

```text
datasets/
└── sample/
    ├── schema.yaml              # Column definitions
    ├── data.csv                 # Source data
    ├── paradedb/
    │   ├── pre.sql              # Create tables, indexes
    │   └── post.sql             # VACUUM, ANALYZE
    ├── elasticsearch/
    │   ├── pre.json             # Create index with mappings
    │   └── post.json            # Refresh, force merge
    ├── opensearch/
    │   ├── pre.json
    │   └── post.json
    ├── clickhouse/
    │   ├── pre.sql
    │   └── post.sql
    ├── mongodb/
    │   ├── pre.json             # Drop collection
    │   └── post.json            # Create search indexes
    └── k6/
        ├── simple.js            # k6 test scripts
        └── search_terms.json    # Search terms data
```

### schema.yaml

```yaml
table: documents
columns:
  id: uuid
  title: text
  content: text
```

### Pre/Post Scripts

Pre and post scripts are defined per dataset in the dataset directory (e.g., `datasets/sample/paradedb/pre.sql`). They run during data loading to set up and optimize each backend.

#### SQL Backends (ParadeDB, PostgreSQL, ClickHouse)

Pre and post scripts are standard SQL executed directly:

```sql
-- pre.sql: Create table and prepare for bulk load
DROP TABLE IF EXISTS documents;
CREATE TABLE documents (
  id BIGINT PRIMARY KEY,
  title TEXT,
  content TEXT
);

-- post.sql: Create indexes after load
CREATE INDEX ON documents USING bm25 (content);
VACUUM ANALYZE documents;
```

#### Elasticsearch / OpenSearch Pre/Post Scripts

**pre.json** - Creates index with mappings (single object, sent to PUT /{index}):

```json
{
  "index": "documents",
  "mappings": {
    "properties": {
      "id": { "type": "keyword" },
      "title": { "type": "text", "analyzer": "english" },
      "content": { "type": "text", "analyzer": "english" }
    }
  },
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 0,
    "refresh_interval": "-1"
  }
}
```

**post.json** - Array of API operations to execute:

```json
[
  {
    "index": "documents",
    "endpoint": "_settings",
    "body": {
      "index": {
        "refresh_interval": "1s"
      }
    }
  },
  {
    "endpoint": "_refresh"
  },
  {
    "endpoint": "_forcemerge",
    "params": {
      "max_num_segments": 1
    }
  }
]
```

Each operation in the array specifies:

- `endpoint` - The API endpoint (e.g., `_settings`, `_refresh`, `_forcemerge`)
- `body` - Optional JSON body (uses PUT method if present)
- `params` - Optional query parameters
- `index` - Optional index override (defaults to "documents")

#### MongoDB

```json
// pre.json: Drop existing collection
{
  "database": "benchmark",
  "collection": "documents",
  "drop": true
}

// post.json: Create search index
{
  "database": "benchmark",
  "collection": "documents",
  "searchIndex": {
    "name": "content_search",
    "definition": {
      "mappings": {
        "dynamic": false,
        "fields": {
          "content": { "type": "string", "analyzer": "lucene.english" }
        }
      }
    }
  }
}
```

## Docker Setup

Docker is **optional**. You can run benchmarks against any database instance — local installs, cloud services, or remote servers. Without Docker, you lose container CPU/memory metrics in the dashboard, but everything else works.

| Service       | Profile         | Port      |
| ------------- | --------------- | --------- |
| paradedb      | `paradedb`      | 5432      |
| postgresfts   | `postgresfts`   | 5433      |
|   | ``  | 5435      |
| elasticsearch | `elasticsearch` | 9200      |
| opensearch    | `opensearch`    | 9201      |
| clickhouse    | `clickhouse`    | 9000/8123 |
| mongodb       | `mongodb`       | 27017     |

For self-signed HTTPS OpenSearch clusters, set `OPENSEARCH_SKIP_TLS_VERIFY=true`.

## License

MIT License - see [LICENSE](LICENSE) for details.
