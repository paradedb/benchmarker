# Dashboard

The real-time dashboard streams benchmark results to a browser-based UI as the test runs.

## Features

- **Latency over time** - P50/P90/P95/P99 percentiles per backend
- **Query throughput** - Queries per second
- **Ingest rate** - Documents inserted per second
- **Container resources** - CPU and memory usage from Docker
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

| Keyword | Effect                                                  |
| ------- | ------------------------------------------------------- |
| `live`  | Serve the real-time dashboard at http://localhost:5665/ |
| `json`  | Write a raw `dashboard_<timestamp>.json` snapshot on exit |
| `html`  | Write a standalone `dashboard_<timestamp>.html` viewer on exit |

```bash
# Default — live dashboard only
./k6 run --out dashboard script.js

# Standalone HTML file only (no server)
./k6 run --out dashboard=html script.js

# Live dashboard + both export files
./k6 run --out dashboard=live,html,json script.js
```

## Export & Replay

The saved JSON keeps raw latency samples so you can re-aggregate with different timeline settings; the HTML is a single-file viewer with the same data embedded.

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

| Metric                   | Type    | Description                     |
| ------------------------ | ------- | ------------------------------- |
| `query_duration`         | Trend   | Query latency in milliseconds   |
| `query_hits`             | Gauge   | Number of results returned      |
| `ingest_duration`        | Trend   | Insert latency in milliseconds  |
| `ingest_docs`            | Counter | Documents inserted              |
| `update_duration`        | Trend   | Update latency in milliseconds  |
| `update_docs`            | Counter | Documents updated               |
| `backend_init`           | Gauge   | Signals a backend is configured |
| `scenario_started`       | Gauge   | Signals a scenario has begun    |
| `container_cpu_percent`  | Gauge   | Container CPU usage percentage  |
| `container_memory_bytes` | Gauge   | Container memory usage          |

## Environment Variables

| Variable                 | Default | Description                                                                        |
| ------------------------ | ------- | ---------------------------------------------------------------------------------- |
| `DASHBOARD_BROADCAST_MS` | `200`   | SSE broadcast interval in milliseconds                                             |
| `DASHBOARD_WINDOW_MS`    | `1000`  | Sliding window for timeline percentile aggregation (0 for non-overlapping buckets) |
