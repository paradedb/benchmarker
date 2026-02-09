# benchmarks

A k6 extension for benchmarking full-text search backends with a unified API, real-time dashboard, and comprehensive data loading tools.

Compare search performance across **ParadeDB**, **PostgreSQL FTS**, ****, **Elasticsearch**, **OpenSearch**, **ClickHouse**, and **MongoDB Atlas Search** with consistent metrics and visualization.

## Features

- **7 Search Backends** - ParadeDB (BM25), PostgreSQL FTS, , Elasticsearch, OpenSearch, ClickHouse, MongoDB
- **Unified API** - Write once, benchmark everywhere
- **Multiple Instances** - Compare different versions or configurations of the same backend
- **Real-time Dashboard** - Live latency graphs, QPS, CPU/memory monitoring
- **Reproducibility** - Pre/post scripts captured in dashboard for full reproducibility
- **Data Loader CLI** - Bulk load CSVs with pre/post SQL/JSON scripts
- **S3 Integration** - Pull datasets directly from S3
- **Container Metrics** - Automatic Docker resource monitoring

## Quick Start

### 1. Start the backends

```bash
# Start the backends you need
docker compose --profile paradedb up -d

# Or multiple backends
docker compose --profile paradedb --profile elasticsearch up -d

# Or all of them
docker compose --profile all up -d
```

### 2. Build

```bash
make        # Builds both k6 and loader
```

Or individually:

```bash
make k6     # Build k6 with the extension
make loader # Build the loader CLI
```

### 3. Load test data

```bash
./bin/loader load ./datasets/sample
```

### 4. Run a benchmark

```bash
./k6 run --out dashboard datasets/sample/k6/simple.js
```

Open http://localhost:5665 to see real-time results.

## Writing k6 Scripts

### Basic Script Structure

```javascript
import search from "k6/x/search";
import { SharedArray } from "k6/data";
import exec from "k6/execution";

// 1. Configure backends
const backends = search.backends({
  datasetPath: "../",  // Path to dataset directory (for pre/post script capture)
  backends: ["paradedb", "elasticsearch"],
});

// 2. Load search terms (shared across all VUs)
const terms = new SharedArray("search_terms", function () {
  return JSON.parse(open("./search_terms.json"));
});

// 3. Helper to rotate through search terms
function getTerm() {
  return terms[exec.vu.iterationInScenario % terms.length];
}

// 4. Define test scenarios
export const options = {
  scenarios: {
    // Metrics collector runs throughout
    metrics_collector: {
      executor: "constant-vus",
      vus: 1,
      duration: "60s",
      exec: "collectMetrics",
    },
    // Search scenario
    search_test: {
      executor: "constant-vus",
      vus: 5,
      duration: "30s",
      exec: "searchQuery",
    },
  },
};

// 5. Collect container metrics (CPU, memory)
export function collectMetrics() {
  backends.collect();
}

// 6. Execute search queries
export function searchQuery() {
  const term = getTerm();
  backends.get("paradedb").search(
    `SELECT id, title FROM documents WHERE content @@@ $1 LIMIT 10`,
    term
  );
}
```

### Backend Configuration

Each backend in the `backends` array can be a string (uses defaults) or an object (full config):

```javascript
const backends = search.backends({
  datasetPath: "../",
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

| Type            | Description                        | Connection Env Var    |
| --------------- | ---------------------------------- | --------------------- |
| `paradedb`      | ParadeDB with pg_search (BM25)     | `PARADEDB_URL`        |
| `postgres-fts`  | PostgreSQL native FTS              | `POSTGRES_FTS_URL`    |
| `` | PostgreSQL with       | `_URL`   |
| `elasticsearch` | Elasticsearch                      | `ELASTICSEARCH_URL`   |
| `opensearch`    | OpenSearch                         | `OPENSEARCH_URL`      |
| `clickhouse`    | ClickHouse                         | `CLICKHOUSE_URL`      |
| `mongodb`       | MongoDB with Atlas Search          | `MONGODB_URL`         |

### Defining Scenarios

Scenarios control how your test runs. Each scenario specifies:

| Option     | Description                                      |
| ---------- | ------------------------------------------------ |
| `executor` | How VUs are scheduled (`constant-vus`, `ramping-vus`, `per-vu-iterations`, etc.) |
| `vus`      | Number of virtual users                          |
| `duration` | How long the scenario runs                       |
| `startTime`| When to start (for sequential tests)             |
| `exec`     | Which function to run                            |
| `tags`     | Custom tags for grouping metrics in charts       |

```javascript
export const options = {
  scenarios: {
    // Always include metrics collector
    metrics_collector: {
      executor: "constant-vus",
      vus: 1,
      duration: "120s",
      exec: "collectMetrics",
    },
    // ParadeDB: 0s - 30s
    pdb_search: {
      executor: "constant-vus",
      vus: 5,
      duration: "30s",
      exec: "pdbSearch",
    },
    // Elasticsearch: 35s - 65s (5s gap for cleanup)
    es_search: {
      executor: "constant-vus",
      vus: 5,
      duration: "30s",
      startTime: "35s",
      exec: "esSearch",
    },
  },
};
```

By default, all metrics appear on the same chart. Use `tags: { chart: "name" }` to separate scenarios into different dashboard charts.

The `metrics_collector` scenario should run for the entire test duration to capture CPU/memory metrics throughout.

### Error Handling

The API validates configuration and fails fast with clear error messages:

```javascript
// Unknown backend type
backends: ["unknown"]
// → panic: backends: unknown backend type 'unknown'. Valid types: [paradedb elasticsearch ...]

// Missing backends array
search.backends({ paradedb: true })
// → panic: backends: 'backends' array is required

// Missing type in object config
backends: [{ alias: "test" }]
// → panic: backends: each backend config must have a 'type' field

// Duplicate alias
backends: ["paradedb", { type: "paradedb" }]
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

Then open http://localhost:5665 in your browser.

### Export & Replay

```bash
# Export dashboard data during run
DASHBOARD_EXPORT=true ./k6 run --out dashboard benchmark.js

# View saved data later
./bin/dashboard-viewer ./dashboard-export.json
```

## Backend Examples

### ParadeDB (BM25)

```javascript
backends.get("paradedb").search(
  `
  SELECT id, title, paradedb.score(id) as score
  FROM documents
  WHERE content @@@ $1
  ORDER BY score DESC
  LIMIT 10
`,
  "search term",
);
```

### PostgreSQL FTS

```javascript
backends.get("postgres-fts").search(
  `
  SELECT id, title, ts_rank(tsv, plainto_tsquery('english', $1)) as score
  FROM documents
  WHERE tsv @@ plainto_tsquery('english', $1)
  ORDER BY score DESC
  LIMIT 10
`,
  "search term",
);
```

### Elasticsearch / OpenSearch

```javascript
backends.get("elasticsearch").search("documents", {
  query: { match: { content: "search term" } },
  size: 10,
});
```

### ClickHouse

```javascript
backends.get("clickhouse").search(`
  SELECT id, title
  FROM documents
  WHERE hasToken(content, 'term')
  LIMIT 10
`);
```

### MongoDB Atlas Search

```javascript
backends.get("mongodb").search("documents", {
  $search: {
    text: { query: "search term", path: "content" },
  },
});
```

### Ingesting Data

To benchmark ingest performance, use the loader to open a document file and insert batches:

```javascript
import search from "k6/x/search";

const backends = search.backends({
  datasetPath: "../",
  backends: ["paradedb", "elasticsearch"],
});

// Load documents from JSON file
const loader = search.loader();
const docs = loader.openDocuments("/path/to/documents.json");

const BATCH_SIZE = 1000;

export function ingestTest() {
  // Get next batch with auto-generated UUIDs
  const batch = docs.nextBatchNewIds(BATCH_SIZE);

  // Insert into any backend
  backends.get("paradedb").insertBatch("documents", batch);
}
```

The `nextBatchNewIds()` method returns documents with new UUID strings in the `id` field, cycling through the source file. This allows continuous ingestion without ID conflicts.

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

### Dataset Structure

```
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
index: documents
collection: documents
database: benchmark
primaryKey: id

columns:
  id: bigint
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

#### Elasticsearch / OpenSearch

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

### Thresholds

```javascript
export const options = {
  thresholds: {
    "search_duration{backend:paradedb}": ["p(95)<50"],
    "search_duration{backend:elasticsearch}": ["p(95)<50"],
    search_hits: ["min>0"],
  },
};
```

## Configuration

### Connection Strings

Set via environment variables:

```bash
export PARADEDB_URL="postgres://postgres:postgres@localhost:5432/benchmark"
export POSTGRES_FTS_URL="postgres://postgres:postgres@localhost:5433/benchmark"
export _URL="postgres://postgres:postgres@localhost:5435/benchmark"
export ELASTICSEARCH_URL="http://localhost:9200"
export OPENSEARCH_URL="http://localhost:9201"
export CLICKHOUSE_URL="clickhouse://default:clickhouse@localhost:9000/default"
export MONGODB_URL="mongodb://localhost:27017"
```

Or configure per-backend:

```javascript
const backends = search.backends({
  datasetPath: "./datasets/sample",
  backends: [
    {
      type: "paradedb",
      connection: "postgres://user:pass@host:5432/db",
    },
    {
      type: "elasticsearch",
      connection: "https://user:pass@cluster.es.amazonaws.com:443",
    },
  ],
});
```

## Docker Setup

Docker is **optional**. You can run benchmarks against any database instance - local installs, cloud services, or remote servers.

Without Docker, you lose container CPU/memory metrics in the dashboard, but everything else works.

### Local Development with Docker

```bash
docker compose --profile paradedb up -d       # Just ParadeDB
docker compose --profile paradedb --profile elasticsearch up -d  # Multiple
docker compose --profile all up -d            # Everything

# Stop
docker compose --profile all down
```

| Service         | Profile         | Port      |
| --------------- | --------------- | --------- |
| paradedb        | `paradedb`      | 5432      |
| postgres-fts    | `postgres-fts`  | 5433      |
|    | `` | 5435      |
| elasticsearch   | `elasticsearch` | 9200      |
| opensearch      | `opensearch`    | 9201      |
| clickhouse      | `clickhouse`    | 9000/8123 |
| mongodb         | `mongodb`       | 27017     |

## Project Structure

```
benchmarks/
├── module.go                    # k6 module registration
├── backends.go                  # Backend configuration for k6
├── backends/
│   ├── driver.go                # Driver interface and registry
│   ├── shared/
│   │   ├── postgres/            # Shared PostgreSQL driver
│   │   └── elastic/             # Shared Elasticsearch/OpenSearch driver
│   ├── paradedb/                # ParadeDB registration
│   ├── postgresfts/             # PostgreSQL FTS registration
│   ├── /            #  registration
│   ├── elasticsearch/           # Elasticsearch registration
│   ├── opensearch/              # OpenSearch registration
│   ├── clickhouse/              # ClickHouse driver
│   └── mongodb/                 # MongoDB driver
├── metrics/                     # Metrics collection and backend config
├── dashboard/                   # Real-time web dashboard
├── loader/                      # k6 data loader module
├── cmd/
│   ├── loader/                  # Data loader CLI
│   └── dashboard-viewer/        # Dashboard replay viewer
├── datasets/                    # Sample datasets
└── docker-compose.yml           # Local development setup
```

## Development

```bash
make              # Build everything
make k6           # Build k6 with extension
make loader       # Build loader CLI
make test         # Run tests
make clean        # Remove build artifacts
```

## License

MIT License - see [LICENSE](LICENSE) for details.
