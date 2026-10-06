# Dashboard

The real-time dashboard streams benchmark results to a browser-based UI as the test runs.

## Features

- **Latency over time** - Mean and P50/P90/P95/P99 latency per backend
- **Query throughput** - Queries per second
- **Ingest rate** - Documents inserted per second
- **Container resources** - CPU and memory usage from Docker
- **Database telemetry** - Baseline-relative backend counters and gauges
- **Update results** - Attempt latency, updated documents, and failures
- **Backend configuration** - Database settings and version info
- **Pre/Post scripts** - Full SQL/JSON scripts used for setup (for reproducibility)
- **Container** - Curated `docker inspect` output (image, command, env, mounts, ports, host config) for each backend container, captured at run start
- **Query patterns** - Actual queries executed per scenario

## Run metadata

Stamp arbitrary metadata into the exported JSON via the `BENCHMARKER_META` env
var. It's written verbatim to a top-level `meta` field — useful for the
commit / version / machine / link details that aren't observable from the
running stack:

```bash
# Inline JSON
BENCHMARKER_META='{"commit":"abc123","version":"v0.23.1"}' \
  ./k6 run --out dashboard script.js

# Or from a file
BENCHMARKER_META=@./run-meta.json \
  ./k6 run --out dashboard script.js
```

The dashboard frontend doesn't render `meta` directly today — it's there for
downstream consumers (publishing tooling, reports) to read out of the JSON.

## Usage

Enable with the `--out dashboard` flag:

```bash
./k6 run --out dashboard script.js
```

Then open http://localhost:5665/static/ in your browser.

### Output modes

`--out dashboard` accepts a comma-separated list of outputs. Each keyword toggles one output independently — order doesn't matter, unknown keywords error out.

| Keyword     | Effect                                                                                            |
| ----------- | ------------------------------------------------------------------------------------------------- |
| `live`      | Serve the real-time dashboard at http://localhost:5665/                                           |
| `json`      | Write a raw `<directory>/<prefix>_<timestamp>.json` snapshot on exit                              |
| `html`      | Write a standalone `<directory>/<prefix>_<timestamp>.html` viewer on exit                         |
| `query_csv` | Write bounded per-`query_id` latency statistics to `<directory>/<prefix>_<timestamp>_queries.csv` |

```bash
# Default — live dashboard only
./k6 run --out dashboard script.js

# Standalone HTML file only (no server)
./k6 run --out dashboard=html script.js

# Live dashboard + all export files
./k6 run --out dashboard=live,html,json,query_csv script.js
```

The export prefix defaults to `dashboard`. Set `DASHBOARD_EXPORT_PREFIX` to a
filename-safe value to change it; letters, numbers, periods, underscores, and
hyphens are accepted. `DASHBOARD_EXPORT_DIR` selects the destination directory
and defaults to the current directory. Missing export directories are created
during output initialization.

### Per-query CSV

`query_csv` ranks individual workload queries rather than scenario-level query
types. Set k6's native `query_id` metric tag immediately before calling a
backend:

```javascript
import exec from "k6/execution";

export function search() {
  const query = queries.next();
  exec.vu.metrics.tags.query_id = String(query.id);
  backends.get("paradedb").query(SQL, query.text);
}
```

Run it with:

```bash
./k6 run --out dashboard=query_csv script.js
```

The CSV has one row per observed `(run, query_id)`, ordered by estimated P50
latency descending. Count, minimum, mean, and maximum are exact. P50, P90, P95,
and P99 use constant-space streaming estimators. Individual latency samples are
never retained for this export, so memory does not grow with benchmark
duration.

Cardinality is bounded across the whole run. The default limit is 100,000
`(run, query_id)` series and each ID is limited to 1,024 bytes. Set
`DASHBOARD_QUERY_CSV_MAX_SERIES` to a different positive limit. If either bound
is exceeded, the partial CSV is still written and k6 exits with an output
error rather than silently presenting incomplete results. Queries without a
`query_id` tag remain in normal dashboard statistics but do not appear in this
CSV.

## Export & Replay

The saved JSON keeps raw latency samples so you can re-aggregate with different timeline settings; the HTML is a single-file viewer with the same data embedded.

When configured by a backend, database snapshots are stored in the top-level
`telemetry` object. Each backend contains one entry per tagged workload window,
with timestamped, baseline-relative counter values and current gauge values.
The live and standalone dashboards group these generic series by provider,
metric family, and unit rather than hardcoding PostgreSQL-specific panels.

Each run with updates contains a bounded live summary and raw `updateMetrics`
series for duration, documents, and errors. Raw samples are retained in JSON for
analysis; standalone HTML keeps the summary without embedding the raw update
arrays into its rendered payload.

```bash
# View saved JSON later
./bin/dashboard-viewer ./dashboard_2026-02-28_12-00-00.json

# Re-export a saved JSON to a standalone HTML file
./bin/dashboard-viewer --export report.html ./dashboard_2026-02-28_12-00-00.json
```

Build the viewer with:

```bash
make viewer
```

## Metrics

The extension emits standard k6 metrics with backend tags:

| Metric                   | Type    | Description                                       |
| ------------------------ | ------- | ------------------------------------------------- |
| `query_duration`         | Trend   | Query latency in milliseconds                     |
| `query_hits`             | Gauge   | Number of results returned                        |
| `ingest_duration`        | Trend   | Insert latency in milliseconds                    |
| `ingest_docs`            | Counter | Documents inserted                                |
| `update_duration`        | Trend   | Update latency in milliseconds                    |
| `update_docs`            | Counter | Documents updated                                 |
| `update_errors`          | Counter | Failed update attempts                            |
| `backend_init`           | Gauge   | Signals a backend is configured                   |
| `scenario_started`       | Gauge   | Signals a scenario has begun                      |
| `warmup_progress`        | Gauge   | Progress through the first stage of a tagged ramp |
| `container_cpu_percent`  | Gauge   | Container CPU usage percentage                    |
| `container_memory_bytes` | Gauge   | Container memory usage                            |

## Environment Variables

| Variable                         | Default     | Description                                                                        |
| -------------------------------- | ----------- | ---------------------------------------------------------------------------------- |
| `DASHBOARD_BROADCAST_MS`         | `200`       | SSE broadcast interval in milliseconds                                             |
| `DASHBOARD_WINDOW_MS`            | `1000`      | Sliding window for timeline percentile aggregation (0 for non-overlapping buckets) |
| `DASHBOARD_EXPORT_DIR`           | `.`         | Directory for JSON, HTML, and query CSV exports                                    |
| `DASHBOARD_EXPORT_PREFIX`        | `dashboard` | Safe filename prefix shared by exported files                                      |
| `DASHBOARD_QUERY_CSV_MAX_SERIES` | `100000`    | Maximum `(run, query_id)` series retained by `query_csv`                           |
