# xk6-search

A k6 extension for benchmarking full-text search backends with a unified API, real-time dashboard, and comprehensive data loading tools.

Compare search performance across **ParadeDB**, **PostgreSQL FTS**, **pg_textsearch**, **Elasticsearch**, **ClickHouse**, and **MongoDB Atlas Search** with consistent metrics and visualization.

## Features

- **6 Search Backends** - ParadeDB (BM25), PostgreSQL FTS, pg_textsearch, Elasticsearch, ClickHouse, MongoDB
- **Unified API** - Write once, benchmark everywhere
- **Real-time Dashboard** - Live latency graphs, QPS, CPU/memory monitoring
- **Data Loader CLI** - Bulk load CSVs with pre/post SQL scripts
- **S3 Integration** - Pull datasets directly from S3
- **Container Metrics** - Automatic Docker resource monitoring
- **Per-Query Breakdown** - Track latencies by scenario and query type

## Quick Start

### 1. Start the backends

```bash
# Start all backends
docker compose --profile all up -d

# Or just the ones you need
docker compose --profile paradedb --profile elasticsearch up -d
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
./k6 run --out dashboard examples/benchmark.js
```

Open http://localhost:5665 to see real-time results.

## Dashboard

The real-time dashboard shows:

- **Latency over time** - P50/P90/P95/P99 percentiles per query
- **Query throughput** - Queries per second
- **Ingest rate** - Documents inserted per second
- **Container resources** - CPU and memory usage from Docker
- **Backend configuration** - Database settings, indexes, and schema

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

## Backends

### ParadeDB (BM25)

Full-text search with BM25 ranking via the `pg_search` extension.

```javascript
const backends = search.backends({ paradedb: true });

backends.paradedb.search(`
  SELECT id, title, paradedb.score(id) as score
  FROM documents
  WHERE content @@@ $1
  ORDER BY score DESC
  LIMIT 10
`, 'search term');
```

### PostgreSQL FTS

Native PostgreSQL full-text search with tsvector/GIN indexes.

```javascript
const backends = search.backends({ 'postgres-fts': true });

backends.postgresFts.search(`
  SELECT id, title, ts_rank(tsv, plainto_tsquery('english', $1)) as score
  FROM documents
  WHERE tsv @@ plainto_tsquery('english', $1)
  ORDER BY score DESC
  LIMIT 10
`, 'search term');
```

### pg_textsearch

PostgreSQL with the pg_textsearch extension for BM25 search.

```javascript
const backends = search.backends({ 'pg-textsearch': true });

backends.textsearch.search(`
  SELECT id, title, content <@> $1 as score
  FROM documents
  ORDER BY score
  LIMIT 10
`, 'search term');
```

### Elasticsearch

Full Elasticsearch Query DSL support.

```javascript
const backends = search.backends({
  elasticsearch: {
    address: 'http://localhost:9200',
    username: 'elastic',
    password: 'changeme'
  }
});

backends.elastic.search('documents', {
  query: { match: { content: 'search term' } },
  size: 10
});
```

### ClickHouse

OLAP database with full-text search capabilities.

```javascript
const backends = search.backends({ clickhouse: true });

backends.clickhouse.search(`
  SELECT id, title
  FROM documents
  WHERE hasToken(content, 'term')
  LIMIT 10
`);
```

### MongoDB Atlas Search

Document search with aggregation pipelines.

```javascript
const backends = search.backends({ mongodb: true });

backends.mongodb.search('documents', {
  $search: {
    text: { query: 'search term', path: 'content' }
  }
});
```

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
├── sample/
│   ├── schema.yaml              # Column definitions
│   ├── data.csv                 # Source data
│   ├── paradedb/
│   │   ├── pre.sql              # Create tables
│   │   └── post.sql             # Create indexes, VACUUM
│   ├── postgres-fts/
│   │   ├── pre.sql
│   │   └── post.sql
│   ├── elasticsearch/
│   │   ├── pre.json             # Create index with mappings
│   │   └── post.json            # Refresh, force merge
│   ├── clickhouse/
│   │   ├── pre.sql
│   │   └── post.sql
│   ├── mongodb/
│   │   ├── pre.json             # Create search indexes
│   │   └── post.json
│   └── k6/
│       └── benchmark.js         # k6 test scripts
```

### schema.yaml

```yaml
table: documents
index: documents
collection: documents
database: benchmark
primaryKey: id

columns:
  id: uuid
  title: text
  content: text
```

## k6 Scripts

### Basic Example

```javascript
import search from 'k6/x/search';

const backends = search.backends({
  paradedb: true,
  elasticsearch: true
});

export const options = {
  scenarios: {
    paradedb_search: {
      executor: 'constant-vus',
      vus: 10,
      duration: '30s',
      exec: 'paradedbSearch'
    },
    elasticsearch_search: {
      executor: 'constant-vus',
      vus: 10,
      duration: '30s',
      exec: 'elasticsearchSearch'
    }
  }
};

export function paradedbSearch() {
  backends.paradedb.search(`
    SELECT id, title FROM documents
    WHERE content @@@ 'test'
    LIMIT 10
  `);
}

export function elasticsearchSearch() {
  backends.elastic.search('documents', {
    query: { match: { content: 'test' } },
    size: 10
  });
}
```

### With Ingest

```javascript
import search from 'k6/x/search';
import { SharedArray } from 'k6/data';

const backends = search.backends({ paradedb: true });
const loader = search.loader();

const docs = loader.openDocuments('./data/documents.json');

export const options = {
  scenarios: {
    query: {
      executor: 'constant-vus',
      vus: 5,
      duration: '60s',
      exec: 'queryTest'
    },
    ingest: {
      executor: 'constant-arrival-rate',
      rate: 10,
      timeUnit: '1s',
      duration: '30s',
      startTime: '30s',
      preAllocatedVUs: 2,
      exec: 'ingestTest'
    }
  }
};

export function queryTest() {
  backends.paradedb.search(`
    SELECT id, title FROM documents
    WHERE content @@@ 'test'
    LIMIT 10
  `);
}

export function ingestTest() {
  const batch = docs.nextBatchNewIds(100);
  backends.paradedb.insertBatch('documents', batch);
}
```

## Metrics

The extension emits standard k6 metrics with backend tags:

| Metric | Type | Description |
|--------|------|-------------|
| `search_duration` | Trend | Search latency in milliseconds |
| `search_hits` | Gauge | Number of results returned |
| `ingest_duration` | Trend | Insert latency in milliseconds |
| `ingest_docs` | Counter | Documents inserted |

### Thresholds

```javascript
export const options = {
  thresholds: {
    'search_duration{backend:paradedb}': ['p(95)<50'],
    'search_duration{backend:elasticsearch}': ['p(95)<50'],
    'search_hits': ['min>0']
  }
};
```

## Configuration

### Connection Strings

Set via environment variables:

```bash
export PARADEDB_URL="postgres://postgres:postgres@localhost:5432/benchmark"
export POSTGRES_FTS_URL="postgres://postgres:postgres@localhost:5433/benchmark"
export PG_TEXTSEARCH_URL="postgres://postgres:postgres@localhost:5435/benchmark"
export ELASTICSEARCH_URL="http://localhost:9200"
export CLICKHOUSE_URL="clickhouse://default:clickhouse@localhost:9000/default"
export MONGODB_URL="mongodb://localhost:27017"
```

Or configure in code:

```javascript
const backends = search.backends({
  paradedb: { connection: 'postgres://...' },
  elasticsearch: {
    address: 'https://...',
    username: 'elastic',
    password: 'secret'
  },
  clickhouse: { connection: 'clickhouse://...' },
  mongodb: { connection: 'mongodb://...', database: 'mydb' }
});
```

### Backend Options

```javascript
search.backends({
  paradedb: {
    connection: 'postgres://...',
    maxConns: 20,
    minConns: 5,
    preparedStatements: true
  },
  elasticsearch: {
    addresses: ['https://node1:9200', 'https://node2:9200'],
    apiKey: 'base64_api_key'
  },
  clickhouse: {
    connection: 'clickhouse://...',
    maxConns: 20,
    minConns: 5
  },
  mongodb: {
    connection: 'mongodb://...',
    database: 'benchmark'
  }
});
```

## Docker Setup

Docker is **optional**. You can run benchmarks against any database instance - local installs, cloud services, or remote servers. Just set the connection strings:

```bash
export PARADEDB_URL="postgres://user:pass@your-server:5432/db"
export ELASTICSEARCH_URL="https://your-cluster.es.amazonaws.com:443"
./k6 run --out dashboard script.js
```

Without Docker, you lose container CPU/memory metrics in the dashboard, but everything else works: latency graphs, QPS, query breakdown, data loading, etc.

### Local Development with Docker

The included `docker-compose.yml` provides all backends with optimized settings. Each backend has its own profile so you only start what you need.

| Service | Profile | Port | Description |
|---------|---------|------|-------------|
| paradedb | `paradedb` | 5432 | ParadeDB (PostgreSQL + pg_search) |
| postgres-fts | `postgres-fts` | 5433 | PostgreSQL 17 with GIN indexes |
| pg-textsearch | `pg-textsearch` | 5435 | PostgreSQL + pg_textsearch |
| elasticsearch | `elasticsearch` | 9200 | Elasticsearch 8.17 |
| clickhouse | `clickhouse` | 9000/8123 | ClickHouse (native/HTTP) |
| mongodb | `mongodb` | 27017 | MongoDB with Atlas Search |

### Start Services

```bash
docker compose up -d                          # Starts nothing (all have profiles)
docker compose --profile paradedb up -d       # Just ParadeDB
docker compose --profile paradedb --profile elasticsearch up -d  # Multiple
docker compose --profile all up -d            # Everything

# Stop
docker compose --profile all down
```

### Resource Limits

All services configured with:
- **CPU**: 4 cores limit, 2 cores reserved
- **Memory**: 8GB limit, 4GB reserved

## S3 Integration

Pull datasets from S3:

```bash
# Uses AWS credentials from environment or ~/.aws/credentials
./bin/loader pull --dataset wikipedia --source s3://fts-bench/datasets/wikipedia/

# Then load normally
./bin/loader load ./datasets/wikipedia
```

Required AWS configuration:
- `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`, or
- `AWS_PROFILE` for named profiles

## Development

```bash
make              # Build everything
make k6           # Build k6 with extension
make loader       # Build loader CLI
make test         # Run tests
make fmt          # Format code
make lint         # Run linter
make clean        # Remove build artifacts
make deps         # Install dependencies
make help         # Show all targets
```

## Project Structure

```
k6-search/
├── module.go              # k6 module registration
├── backends.go            # Backend configuration for k6
├── backends/
│   ├── driver.go          # Driver interface and shared infrastructure
│   ├── postgres/          # PostgreSQL driver (paradedb, postgres-fts, pg-textsearch)
│   ├── elasticsearch/     # Elasticsearch driver
│   ├── clickhouse/        # ClickHouse driver
│   └── mongodb/           # MongoDB driver
├── metrics/               # Metrics collection
├── dashboard/             # Real-time web dashboard
├── loader/                # k6 data loader module
├── cmd/loader/            # Data loader CLI
├── datasets/              # Sample datasets
├── examples/              # k6 script examples
└── docker-compose.yml     # Local development setup
```

## License

MIT
