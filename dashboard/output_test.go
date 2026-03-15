package dashboard

import (
	"math"
	"testing"
	"time"
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
