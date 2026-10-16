# Datasets

A dataset is a self-contained directory with everything needed to load data and run benchmarks against one or more backends.

## Directory Structure

```text
datasets/sample/
├── schema.yaml              # Column names and types
├── data.csv                 # Source data (or data.parquet)
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

## Profiles

A backend supplies the database driver. A profile supplies an index/query
strategy, its connection and container defaults, and its dataset scripts. For
example, both the `postgres` and `paradedb` profiles use the `postgres` driver.
Existing dataset directory names remain valid.

Select a profile with `loader load --backend paradedb ./datasets/sample` or
`{ type: "paradedb" }` in a k6 backend block. The profile
name selects `datasets/<dataset>/<profile>/pre.sql` and `post.sql` (or `.json`
for JSON-based drivers). Query SQL remains in your k6 script.

Global manifests live in `profiles/<name>.yaml`. Built-in manifests are also
embedded in the binaries, so a checkout is not required at runtime. An external
manifest replaces the embedded manifest of the same name. Lookup uses
`./profiles` relative to the process working directory, or `--profiles-dir`
(CLI) / `profilesDir` (k6) when specified. A missing explicitly selected
directory is an error. Registered third-party backends without a manifest can
still be selected as profiles using their registered defaults.

An optional `datasets/<dataset>/<profile>/profile.yaml` replaces individual
top-level fields from the global manifest. Nested fields are **not** deep-merged:
an override containing `extras` replaces the entire `extras` block. Empty
strings and empty lists are honored; `container: ""` disables Docker metrics.

```yaml
backend: postgres
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
| `backend`     | Required                                                               | Registered driver name                                                |
| `container`   | Profile name                                                           | Docker container, or `""` to disable Docker metrics                   |
| `connection`  | None                                                                   | Connection used when no explicit value or environment override is set |
| `envVar`      | Uppercase profile name, hyphens replaced with underscores, plus `_URL` | Connection environment variable; `""` disables it                     |
| `description` | Empty                                                                  | Human-readable description                                            |
| `color`       | Dashboard palette                                                      | Chart color; explicit k6 colors take precedence                       |
| `extras`      | Empty                                                                  | Optional PostgreSQL configuration capture and telemetry settings      |

Profile manifests are the source of default dashboard colors. Legacy backend
configurations use the selected manifest; saved results without colors use
the embedded manifest's color; explicit script colors take precedence. Profiles without a color use
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
`vectorsearch` manifest captures the pg_vectorsearch version and `prism.*`
settings, and `vectorchord` captures extension versions and `vchord*` settings.
All three select `backend: postgres` and declare their index access methods for
telemetry. The [Cohere example](../datasets/cohere-1m/README.md#postgresql-profile-comparison)
shows them together.

The existing sample dataset uses the `postgres` directory for native FTS.
`postgresfts` is also available as a manifest for datasets using that directory
name; it retains `POSTGRES_URL` and the `postgres` container default.

### Compatibility

Existing type names, shorthand strings, `--backend` selections, result keys,
and dataset script paths remain supported. Every selection now resolves a
profile; no ParadeDB driver registration is needed. Built-in defaults work
without a checkout. For the original k6 types, environment connections still
need an explicit `__ENV` value and custom aliases still default their container
to the alias. New profile types use their manifest's environment and container
defaults. External and dataset manifests can explicitly override the defaults.

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
and `vector(n)` (pgvector; ParadeDB and PostgreSQL only).

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

For single-table datasets, source data lives in `data.csv` or `data.parquet`
at the dataset root, or in a `data/` directory holding sharded parquet files;
the loader prefers a single parquet file, then CSV, then the shard directory.
CSV cells hold array and vector values as JSON (e.g. `"[0.1,0.2]"`). Parquet
columns map directly: scalars to their Go types, `list<float>` to `vector(n)`.

Multi-table datasets keep each table's data under `data/`, resolved per table
with the same preference order: `data/<table>.parquet`, `data/<table>.csv`,
then a `data/<table>/` directory of parquet shards. Tables load sequentially
in `tables` order, into the target named by each entry's `table`.

Backend `pre` and `post` scripts may create or index additional tables beyond
those the CLI bulk-import path loads.

## Loader Support Matrix

| Backend       | CSV scalar columns | Parquet scalar columns | `vector(n)` columns        | Multi-table load/drop          | Cross-table (join) queries    |
| ------------- | ------------------ | ---------------------- | -------------------------- | ------------------------------ | ----------------------------- |
| ParadeDB      | Yes                | Yes                    | Yes                        | Yes                            | Yes                           |
| PostgreSQL    | Yes                | Yes                    | Yes                        | Yes                            | Yes                           |
| ClickHouse    | Yes                | Yes                    | No                         | Yes                            | Yes (no BM25 scoring)         |
| Elasticsearch | Yes                | Yes                    | Yes (`dense_vector`)       | Yes (one index per table)      | No                            |
| OpenSearch    | Yes                | Yes                    | Yes (`knn_vector` mapping) | Yes (one index per table)      | No                            |
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
are no multi-document transactions. That makes them a fit when the profile is
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
is still a CSV-only reader for ingest and update profiles and does not apply
`schema.yaml` type conversion. For typed streaming reads from CSV, Parquet, or
Parquet shard directories, use `db.loader().openDocumentStream(path, {schema})`.
See [streaming profiles](scripting.md#streaming-csv-and-parquet-profiles).

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
