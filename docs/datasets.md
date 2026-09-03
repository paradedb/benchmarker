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

| Backend | CSV scalar columns | Parquet scalar columns | `vector(n)` columns |
| --- | --- | --- | --- |
| ParadeDB | Yes | Yes | Yes |
| PostgreSQL | Yes | Yes | Yes |
| ClickHouse | Yes | Yes | No |
| Elasticsearch | Yes | Yes | No |
| OpenSearch | Yes | Yes | No |
| MongoDB | Yes | Yes | No |

The CLI validates vector schemas before running backend `pre` scripts. A dataset
with `vector(n)` columns will fail early for non-PostgreSQL backends instead of
passing pgvector values into drivers that cannot encode them. Vector dimensions
are checked against `vector(n)` before insert.

The k6 `db.loader().openDocuments()` helper is separate from the CLI loader: it
is still a CSV-only reader for ingest and update workloads and does not apply
`schema.yaml` type conversion.

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
