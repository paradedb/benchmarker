# Dashboard

The real-time dashboard streams benchmark results to a browser-based UI as the test runs.

## Features

- **Latency over time** - P50/P90/P95/P99 percentiles per backend
- **Query throughput** - Queries per second
- **Ingest rate** - Documents inserted per second
- **Container resources** - CPU and memory usage from Docker
- **Backend configuration** - Database settings and version info
- **Pre/Post scripts** - Full SQL/JSON scripts used for setup (for reproducibility)
- **Query patterns** - Actual queries executed per scenario

## Usage

Enable with the `--out dashboard` flag:

```bash
./k6 run --out dashboard script.js
```

Then open http://localhost:5665/static/ in your browser.

## Export & Replay

Save benchmark results and view them later, or export as standalone HTML to share.

```bash
# Export dashboard data during run
DASHBOARD_EXPORT=true ./k6 run --out dashboard benchmark.js

# View saved data later (use generated dashboard_<timestamp>.json file)
./bin/dashboard-viewer ./dashboard_2026-02-28_12-00-00.json

# Save as a standalone HTML file
./bin/dashboard-viewer --html report.html ./dashboard_2026-02-28_12-00-00.json
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
| `DASHBOARD_EXPORT`       | `false` | Set to `true` to save a JSON snapshot when the test ends                           |
| `DASHBOARD_BROADCAST_MS` | `200`   | SSE broadcast interval in milliseconds                                             |
| `DASHBOARD_WINDOW_MS`    | `1000`  | Sliding window for timeline percentile aggregation (0 for non-overlapping buckets) |
