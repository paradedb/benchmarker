# Datasets

A dataset is a self-contained directory with everything needed to load data and run benchmarks against one or more backends.

## Directory Structure

```text
datasets/sample/
├── schema.yaml              # Column names and types
├── download.yaml            # Optional S3 corpus references
├── data/
│   └── data.csv             # Source data (or data.parquet / Parquet shards)
├── paradedb/
│   ├── pre.sql              # Create tables, set up schema
│   └── post.sql             # Create indexes, VACUUM ANALYZE
├── postgres/
│   ├── pre.sql
│   └── post.sql
├── elasticsearch/
│   ├── pre.json             # Create index with mappings
│   └── post.json            # Refresh, force merge
├── clickhouse/
│   ├── pre.sql
│   └── post.sql
├── opensearch/
│   ├── pre.json
│   └── post.json
├── mongodb/
│   ├── pre.json             # Drop collection
│   └── post.json            # Create search indexes
└── k6/
    ├── simple.js            # Benchmark scripts
    └── search_terms.json    # Query terms for benchmarks
```

Each backend subdirectory contains pre/post scripts that the loader runs before and after data loading. SQL backends (ParadeDB, PostgreSQL, ClickHouse) use `.sql` files, HTTP backends (Elasticsearch, OpenSearch, MongoDB) use `.json`. You only need directories for the backends you're testing.

The `k6/` directory holds your benchmark scripts and any supporting data like search terms.

## Backends

A backend is a named benchmark target with a driver, connection and container
defaults, and dataset scripts. A driver implements database communication. For
example, `postgres`, `paradedb`, `pg_vectorsearch`, and `vectorchord` are separate
backends that all use the `postgres` driver.

Select a backend with `loader load --backend paradedb ./datasets/sample` or
`{ type: "paradedb" }` in a k6 backend block. The backend
name selects `datasets/<dataset>/<backend>/pre.sql` and `post.sql` (or `.json`
for JSON-based drivers). Query SQL remains in your k6 script.

Global manifests live in `backends/<name>.yaml`. Built-in manifests are also
embedded in the binaries, so a checkout is not required at runtime. An external
manifest replaces the embedded manifest of the same name. Lookup uses
`./backends` relative to the process working directory, or `--backends-dir`
(CLI) / `backendsDir` (k6) when specified. A missing explicitly selected
directory is an error. Every backend requires an external or embedded manifest;
registering a driver alone does not make it runnable. Dataset overrides also
require a global or embedded base manifest.

An optional `datasets/<dataset>/<backend>/backend.yaml` replaces individual
top-level fields from the global manifest. Nested fields are **not** deep-merged:
an override containing `extras` replaces the entire `extras` block. Empty
strings and empty lists are honored; `container: ""` disables Docker metrics.

```yaml
driver: postgres
container: paradedb
connection: postgres://postgres:postgres@localhost:5432/benchmark
envVar: PARADEDB_URL
description: ParadeDB pg_search BM25 index
color: "#7C3AED"
extras:
  gucPrefixes: [paradedb]
  indexAccessMethods: [paradedb, bm25]
  queries:
    - key: paradedb.version
      query: SELECT paradedb.version_info()::text
```

| Field         | Default                                                                | Meaning                                                               |
| ------------- | ---------------------------------------------------------------------- | --------------------------------------------------------------------- |
| `driver`      | Required                                                               | Registered driver name                                                |
| `container`   | None                                                                   | Docker container, or `""` to disable Docker metrics                   |
| `connection`  | None                                                                   | Connection used when no explicit value or environment override is set |
| `envVar`      | Uppercase backend name, hyphens replaced with underscores, plus `_URL` | Connection environment variable; `""` disables it                     |
| `description` | Empty                                                                  | Human-readable description                                            |
| `color`       | Dashboard palette                                                      | Chart color; explicit k6 colors take precedence                       |
| `extras`      | Empty                                                                  | Optional PostgreSQL configuration capture and telemetry settings      |

Omitting `container` from a global manifest disables Docker CPU/memory stats,
as does `container: ""`. In a dataset override, omission inherits the global
value; use `container: ""` to clear it. Custom manifests that previously relied
on an implicit container name must now set `container` explicitly.

For a remote backend such as TIN, omit both `container` and `connection`:

```yaml
driver: postgres
envVar: TIN_URL
description: PostgreSQL TIN (PlanetScale, only available via DBaaS)
color: "#1a1a1a"
```

Set `TIN_URL` or provide an explicit connection (`--connection` in the loader,
`connection` in k6). Without either, TIN fails with an error naming `TIN_URL`.
Drivers supporting another endpoint setting remain an exception: Turbopuffer
can use `TURBOPUFFER_REGION`. Docker stats require a configured container;
query latency, throughput, and supported database telemetry remain available
without one. Remote database telemetry depends on service capabilities and
permissions.

Backend manifests are the source of default dashboard colors. Runs use the
selected manifest; saved results without colors use
the embedded manifest's color; explicit script colors take precedence. Backends without a color use
the dashboard's generic palette.

PostgreSQL extras support:

- `gucs`: individual settings to capture in addition to the driver's standard settings.
- `gucPrefixes`: complete setting namespaces, such as `paradedb`.
- `queries`: SQL strings returning one text column (its column name becomes the
  capture key), or `{ key, query }` mappings with an explicit key. Queries returning
  two text columns capture `(name, value)` rows grouped by the name's dot-prefix.
  Use explicit keys when preserving a historical result format. Cast non-text
  values to text. Each entry should be one SQL statement.
- `indexAccessMethods`: replace the PostgreSQL index-I/O telemetry access methods
  while retaining standard database telemetry. An explicit empty list disables
  index-I/O capture only; omitting the field retains the driver's defaults.

The embedded ParadeDB manifest also captures segment counts and all `paradedb.*`
settings, preserving the existing result keys and telemetry series. The
`pg_vectorsearch` manifest captures the pg_vectorsearch version and `prism.*`
settings, and `vectorchord` captures extension versions and `vchord*` settings.
All three select `driver: postgres` and declare their index access methods for
telemetry. The [Cohere example](../datasets/cohere-1m/README.md#postgresql-backend-comparison)
shows them together.

The existing sample dataset uses the `postgres` directory for native FTS.
`postgresfts` is also available as a manifest for datasets using that directory
name; it retains `POSTGRES_URL` and the `postgres` container default.

### Configuration and migration

All backends use the same connection precedence: explicit connection, then the
manifest's environment variable, then its default connection. A custom alias
changes the result label; the container still comes from the backend manifest
unless explicitly overridden. Turbopuffer can resolve its endpoint from
`TURBOPUFFER_REGION` when no connection URL is supplied.

Manifests use `driver:` and live under `backends/`. Dataset overrides are named
`backend.yaml`; external directories use `--backends-dir` (CLI) or `backendsDir`
(k6). The Timescale backend is named `pg_vectorsearch`, including its dataset
subdirectory, Compose service, and `PG_VECTORSEARCH_URL` /
`PG_VECTORSEARCH_CONTAINER` variables. Rebuild binaries after migrating; embedded
manifests work without a checkout. Driver registration cannot replace a manifest.

## schema.yaml

```yaml
table: documents
columns:
  id: uuid
  title: text
  content: text
  emb: vector(768)
```

Supported column types: `text`/`varchar`, `bigint`, `integer`, `smallint`,
`boolean`, `timestamp`, `jsonb`, `uuid`, arrays of the integer and text types,
and `vector(n)` (ParadeDB, PostgreSQL, Elasticsearch, OpenSearch, and Turbopuffer).

Normalized datasets declare a `tables` list instead of top-level
`table`/`columns` (the two forms cannot be mixed):

```yaml
tables:
  - table: posts
    columns:
      id: integer
      title: text
  - table: comments
    columns:
      id: integer
      post_id: integer
      text: text
```

## Data Files

For single-table datasets, all corpus data lives under `data/`. The loader
resolves `data/data.parquet`, then `data/data.csv`, then Parquet shards directly
under `data/`. Empty shard directories are errors. Root-level `data.csv` or
`data.parquet` causes a migration error even when a valid candidate also exists.
CSV cells hold array and vector values as JSON (e.g. `"[0.1,0.2]"`). Parquet
columns map directly: scalars to their Go types, `list<float>` to `vector(n)`.

Multi-table datasets keep each table's data under `data/`, resolved per table
with the same preference order: `data/<table>.parquet`, `data/<table>.csv`,
then a `data/<table>/` directory of parquet shards. Tables load sequentially
in `tables` order, into the target named by each entry's `table`.

Backend `pre` and `post` scripts may create or index additional tables beyond
those the CLI bulk-import path loads.

A dataset may embed its corpus under `data/` or declare remote corpus
in `download.yaml`. The shared source is
`s3://paradedb-benchmark/datasets/<corpus>/v1/`. Generated `dataset.yaml` records
download provenance and must not be used as source configuration. Keep
benchmark-specific JSON fixtures in `k6/`. Query inputs and ground truth are
managed separately; `download.yaml` only downloads corpus into `data/`. See
[pulling data](loader.md#commands).

## Loader Support Matrix

| Backend       | CSV scalar columns | Parquet scalar columns | `vector(n)` columns        | Multi-table load/drop          | Cross-table (join) queries    |
| ------------- | ------------------ | ---------------------- | -------------------------- | ------------------------------ | ----------------------------- |
| ParadeDB      | Yes                | Yes                    | Yes                        | Yes                            | Yes                           |
| PostgreSQL    | Yes                | Yes                    | Yes                        | Yes                            | Yes                           |
| ClickHouse    | Yes                | Yes                    | No                         | Yes                            | Yes (no BM25 scoring)         |
| Elasticsearch | Yes                | Yes                    | Yes (`dense_vector`)       | Yes (one index per table)      | No                            |
| OpenSearch    | Yes                | Yes                    | Yes (`knn_vector` mapping) | Yes (one index per table)      | No                            |
| Turbopuffer   | Yes                | Yes                    | Yes (`[n]f32` schema)      | Yes (one namespace per table)  | No                            |
| MongoDB       | Yes                | Yes                    | No                         | Yes (one collection per table) | No (driver is `$search`-only) |

The CLI validates vector schemas before running backend `pre` scripts. A dataset
with `vector(n)` columns will fail early for unsupported backends instead of
passing vector values into drivers that cannot encode them. Vector dimensions
are checked against `vector(n)` before insert. For Elasticsearch/OpenSearch the
vector arrives as a plain JSON float array; the backend `pre.json` must define
the matching `dense_vector`/`knn_vector` field, and a schema column named `_id`
becomes the document id rather than a source field.

Consistency caveat for cross-backend comparisons: the Postgres-family backends
are transactional and read-your-writes, while Elasticsearch and OpenSearch are
near-real-time — documents only become searchable after a refresh, and there
are no multi-document transactions. That makes them a fit when the workload is
append-only and absolute correctness of reads isn't required; benchmark
numbers implicitly compare against that weaker consistency model, so loads
always refresh (see each dataset's `post.json`) before queries run.

Multi-table loading works for every backend because type conversion happens in
the shared row-source layer before each driver's insert: SQL backends insert
into the named table, Elasticsearch/OpenSearch bulk into an index named after
the table, and MongoDB into a collection named after it. Whether the loaded
tables can then be _queried together_ depends on the backend: SQL backends
support joins in k6 query scripts, Elasticsearch and OpenSearch have no
cross-index joins, and the MongoDB driver only issues single-`$search`
aggregation pipelines (no `$lookup`).

Column types map to Go values once for all backends (`smallint` → `int16`,
`integer` → `int32`, `bigint` → `int64`, and so on), so backend DDL must use
the matching width — e.g. a `smallint` schema column should be `SMALLINT` in
Postgres-family `pre.sql` and `Int16` in ClickHouse.

The k6 `db.loader().openDocuments()` helper is separate from the CLI loader: it
is still a CSV-only reader for ingest and update workloads and does not apply
`schema.yaml` type conversion. For typed streaming reads from CSV, Parquet, or
Parquet shard directories, use `db.loader().openDocumentStream(path, {schema})`.
See [streaming workloads](scripting.md#streaming-csv-and-parquet-workloads).

## Pre/Post Scripts

Pre and post scripts are defined per dataset in the dataset directory (e.g., `datasets/sample/paradedb/pre.sql`). They run during data loading to set up and optimize each backend.

### SQL Backends (ParadeDB, PostgreSQL, ClickHouse)

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
CREATE INDEX ON documents USING paradedb (content);
VACUUM ANALYZE documents;
```

### Elasticsearch / OpenSearch

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

### MongoDB

**pre.json** - Drop existing collection:

```json
{
  "database": "benchmark",
  "collection": "documents",
  "drop": true
}
```

**post.json** - Create search index:

```json
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
