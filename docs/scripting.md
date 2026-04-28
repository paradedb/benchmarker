# Writing k6 Scripts

Scripts are standard [k6](https://grafana.com/docs/k6/latest/) JavaScript with the `k6/x/database` extension imported. k6 concepts like scenarios, executors, and VUs (virtual users, concurrent goroutines each running your test function in a loop) all apply. This extension adds backend drivers, a phase timer, and Docker metrics on top. You may find the [k6 scenarios documentation](https://grafana.com/docs/k6/latest/using-k6/scenarios/) useful as a reference.

## Basic Script Structure

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
  query_test: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.get(),
    exec: "queryTest",
  },
};

// 4. Add Docker metrics collector
backends.addDockerMetricsCollector(scenarios, timer);
export function collectMetrics() { backends.collect(); }

export const options = { scenarios };

// 5. Execute queries
export function queryTest() {
  const term = terms.next();
  backends
    .get("paradedb")
    .query(
      `SELECT id, title FROM documents WHERE content ||| $1 LIMIT 10`,
      term,
    );
}
```

## Module API

The `db` module (`k6/x/database`) provides:

| Function | Returns | Description |
| --- | --- | --- |
| `db.backends(config)` | `Backends` | Initializes backend drivers and Docker metrics collector from config |
| `db.metrics(config)` | `Collector` | Creates a standalone Docker container metrics collector (use `backends.addDockerMetricsCollector()` instead for most cases) |
| `db.timer({ duration, gap })` | `Timer` | Creates a phase timer for staggering scenarios |
| `db.loader()` | `Loader` | Creates a CSV document reader for ingest or update benchmarks |
| `db.terms(data)` | `Terms` | Loads a JSON array of query strings to avoid caching bias. `terms.next()` cycles sequentially, `terms.random()` picks randomly. Accepts a JSON string via `open()` or a k6 `SharedArray`. |

## Backend Configuration

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
backends.get("paradedb").query(...);
backends.get("paradedb-v2").query(...);
backends.get("elasticsearch").query(...);
```

## Available Backend Types

The framework is database-agnostic - the current backends and datasets are focused on full-text search, but the same infrastructure works for any query workload. See [CONTRIBUTING.md](../CONTRIBUTING.md) for how to add a new backend.

**PostgreSQL-based** (shared driver, different extensions):

| Type            | Description                              |
| --------------- | ---------------------------------------- |
| `paradedb`      | ParadeDB with pg_search (BM25)           |
| `postgresfts`   | PostgreSQL native FTS (tsvector/tsquery) |

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

## Benchmark Patterns

[Scenarios](https://grafana.com/docs/k6/latest/using-k6/scenarios/) control how your test runs. Every benchmark needs at least one query/ingest scenario. Use `backends.addDockerMetricsCollector()` to add Docker CPU/memory monitoring - it adds the scenario and returns the collect function in one call.

### Executors

k6 provides several executors that determine how virtual users are scheduled:

| Executor                 | Description                                                    | Best for                             |
| ------------------------ | -------------------------------------------------------------- | ------------------------------------ |
| `constant-vus`           | Fixed number of VUs running for a set duration                 | Most query benchmarks                |
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

### Pattern 1: Single Backend

The simplest benchmark - one backend, constant load. No timer needed:

```javascript
const scenarios = {
  pdb_query: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    exec: "pdbQuery",
  },
};
backends.addDockerMetricsCollector(scenarios, "35s");
export function collectMetrics() { backends.collect(); }

export const options = { scenarios };

export function pdbQuery() {
  backends.get("paradedb").query(
    `SELECT id, title FROM documents WHERE content ||| $1 LIMIT 10`,
    terms.next(),
  );
}
```

### Pattern 2: Sequential Multi-Backend Comparison

Use `db.timer()` to stagger backends so they don't compete for system resources. `advanceAndGet()` returns a startTime string and advances; `get()` returns the same phase (for parallel scenarios):

```javascript
const timer = db.timer({ duration: "30s", gap: "5s" });

const scenarios = {
  // get() returns the current phase's startTime ("0s")
  pdb_query: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.get(),
    exec: "pdbQuery",
  },
  // get() again - parallel scenario in the same phase
  pdb_count: {
    executor: "constant-vus",
    vus: 3,
    duration: "30s",
    startTime: timer.get(),
    exec: "pdbCount",
  },
  // advanceAndGet() moves to the next phase ("35s")
  es_query: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.advanceAndGet(),
    exec: "esQuery",
  },
  es_count: {
    executor: "constant-vus",
    vus: 3,
    duration: "30s",
    startTime: timer.get(),
    exec: "esCount",
  },
};

// Adds a metrics_collector scenario covering the full test duration
backends.addDockerMetricsCollector(scenarios, timer);
export function collectMetrics() { backends.collect(); }

export const options = { scenarios };
```

- `get()` - returns the current phase's startTime (auto-advances on first call); use for the first scenario and for parallel scenarios in the same phase
- `advanceAndGet()` / `next()` - advances to the next phase and returns its startTime; use when starting a new phase
- `backends.addDockerMetricsCollector(scenarios, timer)` - adds a `metrics_collector` scenario covering the full test duration
- Also accepts a duration string: `backends.addDockerMetricsCollector(scenarios, "500s")`

### Pattern 3: Parallel Query + Ingest

Run queries and ingest in parallel within each phase to measure how query latency holds up under concurrent write load. Each backend gets its own query function (since query syntax differs), while `env` shares the ingest function. Use `constant-arrival-rate` for ingest to maintain a predictable insertion rate:

```javascript
import db from "k6/x/database";

const backends = db.backends({ backends: ["paradedb", "elasticsearch"] });
const terms = db.terms(open("./search_terms.json"));
const loader = db.loader();
const docs = loader.openDocuments("../ingest_data.csv");
const BATCH_SIZE = 1000;

const timer = db.timer({ duration: "30s", gap: "5s" });

const scenarios = {
  // ParadeDB: query + ingest in parallel
  pdb_query: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.get(),
    exec: "pdbQuery",
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
  // Elasticsearch: query + ingest in parallel
  es_query: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.advanceAndGet(),
    exec: "esQuery",
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
backends.addDockerMetricsCollector(scenarios, timer);
export function collectMetrics() { backends.collect(); }

export const options = { scenarios };

export function pdbQuery() {
  backends.get("paradedb").query(
    `SELECT id, title FROM documents WHERE content ||| $1 LIMIT 10`,
    terms.next(),
  );
}

export function esQuery() {
  backends.get("elasticsearch").query("documents", {
    query: { match: { content: terms.next() } },
    size: 10,
  });
}

export function ingest() {
  const backend = __ENV.BACKEND;
  const batch = docs.nextBatch(BATCH_SIZE, backend);
  backends.get(backend).insertBatch("documents", batch);
}
```

### Pattern 4: Query Shape Comparison with Chart Tags

Compare different query types across backends. Use `tags: { chart: "name" }` to group scenarios into separate dashboard charts:

```javascript
const timer = db.timer({ duration: "30s", gap: "2s" });

const scenarios = {
  // Single term TopK - grouped on one chart
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
  // Count queries - grouped on a separate chart
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
backends.addDockerMetricsCollector(scenarios, timer);
export function collectMetrics() { backends.collect(); }

export const options = { scenarios };
```

Scenarios with the same `chart` tag appear on the same dashboard chart. Scenarios without a `chart` tag all share the default chart.

### Pattern 5: Ramping Load

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
    exec: "queryTest",
  },
};
backends.addDockerMetricsCollector(scenarios, "220s");
export function collectMetrics() { backends.collect(); }

export const options = { scenarios };
```

## Ingest Benchmarks

The primary purpose of ingest scenarios is to put write pressure on the database while queries are running, simulating realistic mixed workloads where the index is being updated concurrently with queries. This is more useful for measuring how query latency degrades under write load than for comparing raw ingest throughput across backends, since each database handles write consistency, indexing, and flush semantics differently.

To run an ingest workload, use the loader to open a document file and insert batches. The data file used for ingest must contain documents that are not already in the database. If you pre-loaded a dataset with the loader CLI or a setup function, you will need a separate CSV file (e.g. `ingest_data.csv`) with different document IDs for insert scenarios, otherwise you will get duplicate key errors. Note that you can only run an ingest benchmark once per file, since subsequent runs will fail with duplicates after the documents have been inserted. Use scenario `env` to pass the backend name so one function handles all backends. The second argument to `nextBatch()` is a pool key: each pool has its own atomic counter, so VUs within a backend get non-overlapping batches, while different backends independently walk through the same data from the start.

See [Pattern 3](#pattern-3-parallel-query--ingest) for a complete example combining queries and ingest.

## Update Benchmarks

Like ingest, update scenarios are primarily useful for stressing the database during query runs, measuring how query performance changes when existing documents are being modified concurrently. Direct comparison of update throughput across backends is not meaningful since each handles row versioning, re-indexing, and consistency differently.

The data file you open for updates must contain documents that already exist in the database. You can use the same file you loaded originally, or a cut-down version of it if the full dataset is large. Like ingest, this is a run-once operation; after the swapped documents have been written, running the same benchmark again will produce no meaningful changes since the values are already swapped.

```javascript
export function updateTest() {
  const backend = __ENV.BACKEND;
  const batch = docs.nextBatchSwapped(BATCH_SIZE, "content", backend);
  backends.get(backend).updateBatch("documents", batch);
}
```

The `nextBatchSwapped()` method lazily builds a copy of all documents with adjacent values of the given field swapped, then paginates through them atomically. The optional third argument is a pool key, same as `nextBatch()`.

## Additional Backend Methods

Each backend returned by `backends.get()` also exposes:

| Method | Description |
| --- | --- |
| `setTimeout(seconds)` | Set the query timeout for this backend |
| `insert(table, doc)` | Insert a single document |
| `update(table, doc)` | Update a single document (keyed by `id` or `_id`) |

Call `backends.setTimeout(seconds)` to set the timeout on all backends at once, or `backends.close()` to close all connections.

## Loading Data from k6 Scripts

The loader can also bulk-load data directly from a k6 script (without the CLI). This is useful for setup functions or ingest benchmarks that need to pre-populate data:

```javascript
const loader = db.loader();

// Generic: specify backend name and connection string
loader.load("paradedb", "postgres://postgres:postgres@localhost:5432/benchmark", {
  file: "../data.csv",
  table: "documents",
  dataset: "../",
  batchSize: 10000,
});

// Backend-specific helpers
loader.loadParadeDB("postgres://...", { file: "../data.csv", dataset: "../" });
loader.loadPostgresFTS("postgres://...", { file: "../data.csv", dataset: "../" });
loader.loadElasticsearch({ file: "../data.csv", dataset: "../" });
loader.loadClickHouse("clickhouse://...", { file: "../data.csv", dataset: "../" });
loader.loadMongoDB("mongodb://...", { file: "../data.csv", dataset: "../" });
```

Returns `{ loaded, loadTimeMs, indexTimeMs, totalTimeMs, error }`. The `dataset` path points to the dataset directory containing backend-specific `pre`/`post` scripts.

## Backend Query Reference

### ParadeDB

```javascript
backends.get("paradedb").query(
  `SELECT id, title, pdb.score(id) as score
   FROM documents
   WHERE content ||| $1
   ORDER BY score DESC
   LIMIT 10`,
  "search term",
);
```

### PostgreSQL FTS

```javascript
backends.get("postgresfts").query(
  `SELECT id, title, ts_rank(tsv, plainto_tsquery('english', $1)) as score
   FROM documents
   WHERE tsv @@ plainto_tsquery('english', $1)
   ORDER BY score DESC
   LIMIT 10`,
  "search term",
);
```

### Elasticsearch / OpenSearch

```javascript
backends.get("elasticsearch").query("documents", {
  query: { match: { content: "search term" } },
  size: 10,
});
```

### ClickHouse

```javascript
backends.get("clickhouse").query(
  `SELECT id, title
   FROM documents
   WHERE hasToken(content, 'term')
   LIMIT 10`,
);
```

### MongoDB Atlas Search

```javascript
backends.get("mongodb").query(
  JSON.stringify({
    text: { query: "search term", path: ["content"] },
  }),
  "documents",
);
```

## Error Handling

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
