package dashboard

import (
	"encoding/csv"
	"math"
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"

	k6metrics "go.k6.io/k6/metrics"
	"go.k6.io/k6/output"
)

func TestStreamingQuantileIsExactForInitialSamplesAndBoundedForLongRuns(t *testing.T) {
	initial := newStreamingQuantile(0.99)
	for _, value := range []float64{5, 1, 4, 2, 3} {
		initial.record(value)
	}
	if got := initial.value(); got != 5 {
		t.Fatalf("initial p99 = %v, want exact nearest-rank value 5", got)
	}

	for _, tc := range []struct {
		quantile float64
		want     float64
	}{
		{0.50, 5_000},
		{0.90, 9_000},
		{0.95, 9_500},
		{0.99, 9_900},
	} {
		estimator := newStreamingQuantile(tc.quantile)
		for value := 1; value <= 10_000; value++ {
			estimator.record(float64(value))
		}
		if got := estimator.value(); math.Abs(got-tc.want) > tc.want*0.01 {
			t.Errorf("q%.0f = %.3f, want %.3f within 1%%", tc.quantile*100, got, tc.want)
		}
	}

	if size := unsafe.Sizeof(queryCSVStats{}); size > 1_024 {
		t.Fatalf("query CSV accumulator is %d bytes; expected at most 1024", size)
	}
}

func TestQueryCSVStatsKeepExactCountMeanMinAndMax(t *testing.T) {
	stats := newQueryCSVStats()
	for _, value := range []float64{10, 20, 5, 25} {
		stats.record(value)
	}
	if stats.count != 4 || stats.min != 5 || stats.max != 25 {
		t.Fatalf("exact statistics = count %d min %v max %v", stats.count, stats.min, stats.max)
	}
	if got := stats.sum / float64(stats.count); got != 15 {
		t.Fatalf("mean = %v, want 15", got)
	}
}

func TestRecordQueryCSVEnforcesGlobalSeriesAndIDBounds(t *testing.T) {
	o := &Output{queryCSVMaxSeries: 1}
	run := &RunMetrics{}
	o.recordQueryCSV(run, "one", 10)
	o.recordQueryCSV(run, "one", 20)
	o.recordQueryCSV(run, "two", 30)
	o.recordQueryCSV(run, strings.Repeat("x", maxQueryCSVQueryIDBytes+1), 40)

	if o.queryCSVSeries != 1 || run.QueryCSV["one"].count != 2 {
		t.Fatalf("bounded series = %d, first stats = %#v", o.queryCSVSeries, run.QueryCSV["one"])
	}
	if _, exists := run.QueryCSV["two"]; exists {
		t.Fatal("series beyond the configured cap was retained")
	}
	if o.queryCSVDropped != 2 {
		t.Fatalf("dropped samples = %d, want 2", o.queryCSVDropped)
	}
}

func TestQueryCSVCollectsTaggedDurationsAndRanksSlowestMedianFirst(t *testing.T) {
	registry := k6metrics.NewRegistry()
	duration, err := registry.NewMetric("query_duration", k6metrics.Trend, k6metrics.Time)
	if err != nil {
		t.Fatalf("create query duration metric: %v", err)
	}
	baseTags := registry.RootTagSet().With("backend", "paradedb").With("scenario", "search")
	o := &Output{
		exportQueryCSV:    true,
		queryCSVMaxSeries: 10,
		data: &DashboardData{
			StartTime:  time.Unix(0, 0),
			Runs:       make(map[string]*RunMetrics),
			Containers: make(map[string]*ContainerMetrics),
		},
	}
	for index, sample := range []struct {
		queryID string
		latency float64
	}{
		{"fast", 10},
		{"slow", 100},
		{"fast", 10},
		{"slow", 100},
	} {
		o.AddMetricSamples([]k6metrics.SampleContainer{k6metrics.Samples{{
			TimeSeries: k6metrics.TimeSeries{Metric: duration, Tags: baseTags.With("query_id", sample.queryID)},
			Time:       time.UnixMilli(int64(index + 1)),
			Value:      sample.latency,
		}}})
	}
	o.flush()

	run := o.data.Runs["paradedb"]
	if run == nil || run.QueryCSV["fast"].count != 2 || run.QueryCSV["slow"].count != 2 {
		t.Fatalf("query CSV stats = %#v", run)
	}
	encoded, err := marshalQueryCSV(o.data)
	if err != nil {
		t.Fatalf("marshal query CSV: %v", err)
	}
	records, err := csv.NewReader(strings.NewReader(string(encoded))).ReadAll()
	if err != nil {
		t.Fatalf("read query CSV: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("CSV row count = %d, want header plus two rows\n%s", len(records), encoded)
	}
	if got := strings.Join(records[0], ","); got != "rank,run,backend,query_id,count,min_ms,mean_ms,p50_ms,p90_ms,p95_ms,p99_ms,max_ms" {
		t.Fatalf("CSV header = %q", got)
	}
	if records[1][3] != "slow" || records[1][4] != "2" || records[1][6] != "100.000" {
		t.Fatalf("first CSV data row = %v, want slow query", records[1])
	}
	if records[2][3] != "fast" || records[2][6] != "10.000" {
		t.Fatalf("second CSV data row = %v, want fast query", records[2])
	}
}

func TestQueryCSVStopReportsCardinalityOverflowAfterWritingPartialCSV(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DASHBOARD_EXPORT_DIR", dir)
	t.Setenv("DASHBOARD_QUERY_CSV_MAX_SERIES", "1")

	raw, err := New(output.Params{ConfigArgument: "query_csv"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	o := raw.(*Output)
	if err := o.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	registry := k6metrics.NewRegistry()
	duration, err := registry.NewMetric("query_duration", k6metrics.Trend, k6metrics.Time)
	if err != nil {
		t.Fatalf("create query duration metric: %v", err)
	}
	baseTags := registry.RootTagSet().With("backend", "paradedb")
	for index, queryID := range []string{"one", "two"} {
		o.AddMetricSamples([]k6metrics.SampleContainer{k6metrics.Samples{{
			TimeSeries: k6metrics.TimeSeries{Metric: duration, Tags: baseTags.With("query_id", queryID)},
			Time:       time.UnixMilli(int64(index + 1)),
			Value:      float64(index + 1),
		}}})
	}
	if err := o.Stop(); err == nil || !strings.Contains(err.Error(), "dropped 1 query samples") {
		t.Fatalf("Stop error = %v, want cardinality overflow", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read export directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "_queries.csv") {
			return
		}
	}
	t.Fatal("partial query CSV was not written")
}
