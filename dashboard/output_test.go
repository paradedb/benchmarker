package dashboard

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/paradedb/benchmarker/metrics"
)

func TestUpdateIngestRateSkipsInitialPoint(t *testing.T) {
	o := &Output{}
	rm := &RunMetrics{TotalIngested: 100}

	o.updateIngestRate(rm, 1000)

	if len(rm.IngestRate) != 0 {
		t.Fatalf("expected no ingest-rate points on first sample, got %d", len(rm.IngestRate))
	}
	if rm.LastIngestTime != 1000 {
		t.Fatalf("expected first ingest timestamp to be recorded, got %d", rm.LastIngestTime)
	}
	if rm.LastIngestDocs != 100 {
		t.Fatalf("expected first ingest doc count to be recorded, got %d", rm.LastIngestDocs)
	}
}

func TestGetSummaryUsesFirstIngestTimeForAverageRate(t *testing.T) {
	o := &Output{
		data: &DashboardData{
			StartTime: time.Unix(0, 0),
			Runs: map[string]*RunMetrics{
				"ingest": {
					Name:            "ingest",
					Backend:         "paradedb",
					Queries:         map[string]*QueryMetrics{},
					TotalIngested:   300,
					FirstIngestTime: 1000,
					EndTime:         3000,
				},
			},
		},
	}

	summary := o.getSummary()
	runs := summary["runs"].(map[string]interface{})
	run := runs["ingest"].(map[string]interface{})

	got := run["avgIngestRate"].(float64)
	if math.Abs(got-150) > 0.001 {
		t.Fatalf("expected ingest rate of 150 docs/sec, got %.3f", got)
	}
}

func TestGetSummaryUsesQueryWindowForQPS(t *testing.T) {
	latencies := make([]float64, 10)
	for i := range latencies {
		latencies[i] = 100
	}

	o := &Output{
		data: &DashboardData{
			StartTime: time.Unix(0, 0),
			Runs: map[string]*RunMetrics{
				"search": {
					Name:      "search",
					Backend:   "paradedb",
					StartTime: 1000,
					EndTime:   21000,
					Queries: map[string]*QueryMetrics{
						"search_query": {
							Name:      "search_query",
							Latencies: latencies,
							StartTime: 1000,
							EndTime:   6000,
						},
					},
				},
			},
		},
	}

	summary := o.getSummary()
	runs := summary["runs"].(map[string]interface{})
	run := runs["search"].(map[string]interface{})
	queries := run["queries"].(map[string]interface{})
	query := queries["search_query"].(map[string]interface{})

	got := query["qps"].(float64)
	if math.Abs(got-2.0) > 0.001 {
		t.Fatalf("expected query qps of 2.0, got %.3f", got)
	}
}

func TestGetSummaryEmitsTopLevelBackendsBlock(t *testing.T) {
	metrics.RegisterBackendOptions("paradedb", &metrics.BackendOptions{
		Container: "paradedb",
		Alias:     "paradedb",
		Color:     "#7c3aed",
	})
	metrics.RegisterBackendConfig("paradedb", map[string]interface{}{
		"version": "PostgreSQL 18.0",
	})
	t.Cleanup(func() {
		metrics.RegisterBackendConfig("paradedb", nil)
	})

	o := &Output{
		data: &DashboardData{
			StartTime: time.Unix(0, 0),
			Runs: map[string]*RunMetrics{
				"paradedb_simple": {
					Name:    "paradedb_simple",
					Backend: "paradedb",
					Queries: map[string]*QueryMetrics{},
				},
			},
		},
	}

	summary := o.getSummary()

	// Top-level backends block exists and carries the deduplicated config.
	backends, ok := summary["backends"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected top-level backends block, got %T", summary["backends"])
	}
	paradedb, ok := backends["paradedb"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected backends[paradedb], got %T", backends["paradedb"])
	}
	cfg, ok := paradedb["config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected backends[paradedb].config map")
	}
	if cfg["version"] != "PostgreSQL 18.0" {
		t.Fatalf("expected version in config, got %v", cfg["version"])
	}

	// Per-run entries no longer carry config / containerLimits.
	run := summary["runs"].(map[string]interface{})["paradedb_simple"].(map[string]interface{})
	if _, present := run["config"]; present {
		t.Fatalf("expected run.config to be stripped (now top-level)")
	}
	if _, present := run["containerLimits"]; present {
		t.Fatalf("expected run.containerLimits to be stripped (now under container_info)")
	}
}

func TestReadMetaEnvInline(t *testing.T) {
	t.Setenv("BENCHMARKER_META", `{"commit":"abc123","version":"v0.23.1"}`)
	m := readMetaEnv()
	if m["commit"] != "abc123" {
		t.Fatalf("expected commit=abc123, got %v", m["commit"])
	}
	if m["version"] != "v0.23.1" {
		t.Fatalf("expected version=v0.23.1, got %v", m["version"])
	}
}

func TestReadMetaEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")
	if err := os.WriteFile(path, []byte(`{"commit":"xyz","links":["https://example.com"]}`), 0644); err != nil {
		t.Fatalf("write meta file: %v", err)
	}
	t.Setenv("BENCHMARKER_META", "@"+path)
	m := readMetaEnv()
	if m["commit"] != "xyz" {
		t.Fatalf("expected commit=xyz, got %v", m["commit"])
	}
	links, ok := m["links"].([]interface{})
	if !ok || len(links) != 1 || links[0] != "https://example.com" {
		t.Fatalf("expected links=[example.com], got %v", m["links"])
	}
}

func TestReadMetaEnvUnsetReturnsNil(t *testing.T) {
	t.Setenv("BENCHMARKER_META", "")
	if m := readMetaEnv(); m != nil {
		t.Fatalf("expected nil for unset env, got %v", m)
	}
}
