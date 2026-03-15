package dashboard

import (
	"math"
	"testing"
	"time"

	"github.com/paradedb/benchmarks/metrics"
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

func TestRecordRunActivityReopensEndedRun(t *testing.T) {
	rm := &RunMetrics{
		StartTime: 1000,
		EndTime:   2000,
	}

	recordRunActivity(rm, 3000, 3500)

	if rm.EndTime != 0 {
		t.Fatalf("expected run to reopen on new activity, got end time %d", rm.EndTime)
	}
	if rm.LastUpdateTime != 3500 {
		t.Fatalf("expected last update time to be recorded, got %d", rm.LastUpdateTime)
	}
}

func TestRegisterContainerClearsIdentityForSharedContainer(t *testing.T) {
	containers := map[string]*ContainerMetrics{}

	registerContainer(containers, "shared", "paradedb", &metrics.BackendOptions{
		Alias: "paradedb-a",
		Color: "#111111",
	}, 1000)
	registerContainer(containers, "shared", "elasticsearch", &metrics.BackendOptions{
		Alias: "es-b",
		Color: "#222222",
	}, 2000)

	cm := containers["shared"]
	if cm == nil {
		t.Fatal("expected container metrics entry to be created")
	}
	if !cm.Shared {
		t.Fatal("expected shared container to be marked ambiguous")
	}
	if cm.Backend != "" || cm.Alias != "" || cm.Color != "" {
		t.Fatalf("expected shared container identity to be cleared, got backend=%q alias=%q color=%q", cm.Backend, cm.Alias, cm.Color)
	}
	if cm.Start != 1000 {
		t.Fatalf("expected earliest container start time to be preserved, got %d", cm.Start)
	}
}
