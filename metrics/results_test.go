package metrics

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/grafana/sobek"
	"go.k6.io/k6/js/common"
	"go.k6.io/k6/lib"
	"go.k6.io/k6/lib/executor"
	"go.k6.io/k6/lib/types"
	k6metrics "go.k6.io/k6/metrics"
	"gopkg.in/guregu/null.v3"
)

type fakeVU struct {
	ctx   context.Context
	state *lib.State
}

func (f fakeVU) Context() context.Context {
	if f.ctx != nil {
		return f.ctx
	}
	return context.Background()
}

func (fakeVU) Events() common.Events { return common.Events{} }

func (fakeVU) InitEnv() *common.InitEnvironment { return nil }

func (f fakeVU) State() *lib.State { return f.state }

func (fakeVU) Runtime() *sobek.Runtime { return nil }

func (fakeVU) RegisterCallback() func(func() error) {
	return func(func() error) {}
}

func TestCaptureQueryPatternUsesExplicitBackend(t *testing.T) {
	oldPatterns := QueryPatterns
	QueryPatterns = make(map[string]string)
	defer func() {
		QueryPatterns = oldPatterns
	}()

	registry := k6metrics.NewRegistry()
	state := &lib.State{
		Tags: lib.NewVUStateTags(
			registry.RootTagSet().
				With("scenario", "search_scenario").
				With("chart", "primary"),
		),
	}

	CaptureQueryPattern(fakeVU{state: state}, "paradedb", "SELECT 1")

	got := GetQueryPattern("paradedb", "primary", "search_scenario")
	if got != "SELECT 1" {
		t.Fatalf("expected captured query for explicit backend, got %q", got)
	}
}

func TestQueryResultPreservesQueryIDTag(t *testing.T) {
	registry := k6metrics.NewRegistry()
	oldDuration, oldHits := queryDuration, queryHits
	var err error
	queryDuration, err = registry.NewMetric("query_duration_test", k6metrics.Trend, k6metrics.Time)
	if err != nil {
		t.Fatalf("create query duration metric: %v", err)
	}
	queryHits, err = registry.NewMetric("query_hits_test", k6metrics.Gauge)
	if err != nil {
		t.Fatalf("create query hits metric: %v", err)
	}
	t.Cleanup(func() {
		queryDuration, queryHits = oldDuration, oldHits
	})

	samples := make(chan k6metrics.SampleContainer, 2)
	state := &lib.State{
		Samples: samples,
		Tags: lib.NewVUStateTags(
			registry.RootTagSet().With("query_id", "source-7:phrase"),
		),
	}
	(&QueryResult{Hits: 10, LatencyMs: 12.5}).Emit(context.Background(), fakeVU{state: state}, "paradedb")

	for range 2 {
		for _, sample := range (<-samples).GetSamples() {
			if queryID, ok := sample.Tags.Get("query_id"); !ok || queryID != "source-7:phrase" {
				t.Fatalf("query_id tag = %q, %v", queryID, ok)
			}
		}
	}
}

func warmupTestVU(registry *k6metrics.Registry, start time.Time, tagged bool) fakeVU {
	config := executor.NewRampingVUsConfig("search")
	config.Stages = []executor.Stage{
		{Duration: types.NewNullDuration(10*time.Second, true), Target: null.NewInt(5, true)},
		{Duration: types.NewNullDuration(20*time.Second, true), Target: null.NewInt(5, true)},
	}
	tags := registry.RootTagSet().With("scenario", "search")
	if tagged {
		tags = tags.With("warmup", "true")
	}
	state := &lib.State{
		Tags: lib.NewVUStateTags(tags),
		Options: lib.Options{Scenarios: lib.ScenarioConfigs{
			"search": config,
		}},
	}
	ctx := lib.WithScenarioState(context.Background(), &lib.ScenarioState{
		Name:      "search",
		StartTime: start,
	})
	return fakeVU{ctx: ctx, state: state}
}

func TestWarmupPhaseUsesFirstNativeRampStage(t *testing.T) {
	registry := k6metrics.NewRegistry()
	start := time.Now().Add(-5 * time.Second)
	phase := GetWarmupPhase(warmupTestVU(registry, start, true))

	if !phase.Active {
		t.Fatal("first ramp stage was not recognized as warmup")
	}
	if !phase.Deadline.Equal(start.Add(10 * time.Second)) {
		t.Fatalf("deadline = %s, want %s", phase.Deadline, start.Add(10*time.Second))
	}
	if progress := phase.ProgressAt(time.Now()); math.Abs(progress-50) > 1 {
		t.Fatalf("progress = %v, want approximately 50", progress)
	}

	if phase := GetWarmupPhase(warmupTestVU(registry, time.Now().Add(-10*time.Second), true)); phase.Active {
		t.Fatal("second ramp stage was treated as warmup")
	}
	if phase := GetWarmupPhase(warmupTestVU(registry, start, false)); phase.Active {
		t.Fatal("untagged ramp stage was treated as warmup")
	}
}

func TestTaggedScenarioResultsEmitAfterWarmup(t *testing.T) {
	registry := k6metrics.NewRegistry()
	oldDuration, oldHits := queryDuration, queryHits
	var err error
	queryDuration, err = registry.NewMetric("query_duration_warmup_test", k6metrics.Trend, k6metrics.Time)
	if err != nil {
		t.Fatalf("create query duration metric: %v", err)
	}
	queryHits, err = registry.NewMetric("query_hits_warmup_test", k6metrics.Gauge)
	if err != nil {
		t.Fatalf("create query hits metric: %v", err)
	}
	t.Cleanup(func() {
		queryDuration, queryHits = oldDuration, oldHits
	})

	samples := make(chan k6metrics.SampleContainer, 2)
	state := &lib.State{
		Samples: samples,
		Tags: lib.NewVUStateTags(
			registry.RootTagSet().
				With("scenario", "warm_search").
				With("warmup", "true"),
		),
	}
	ctx := context.Background()
	vu := fakeVU{ctx: ctx, state: state}

	(&QueryResult{Hits: 10, LatencyMs: 12.5}).Emit(ctx, vu, "paradedb")

	if len(samples) != 2 {
		t.Fatalf("tagged measured stage emitted %d samples, want query duration and hits", len(samples))
	}
	for range 2 {
		sample := (<-samples).GetSamples()[0]
		if backend, ok := sample.Tags.Get("backend"); !ok || backend != "paradedb" {
			t.Fatalf("backend tag = %q, %v", backend, ok)
		}
	}
}

func TestUpdateResultEmitsDurationForEveryAttemptAndCountsOutcome(t *testing.T) {
	registry := k6metrics.NewRegistry()
	var err error
	oldDuration, oldDocs, oldErrors := updateDuration, updateDocs, updateErrors
	updateDuration, err = registry.NewMetric("update_duration_test", k6metrics.Trend, k6metrics.Time)
	if err != nil {
		t.Fatalf("create update duration metric: %v", err)
	}
	updateDocs, err = registry.NewMetric("update_docs_test", k6metrics.Counter)
	if err != nil {
		t.Fatalf("create update docs metric: %v", err)
	}
	updateErrors, err = registry.NewMetric("update_errors_test", k6metrics.Counter)
	if err != nil {
		t.Fatalf("create update errors metric: %v", err)
	}
	t.Cleanup(func() {
		updateDuration, updateDocs, updateErrors = oldDuration, oldDocs, oldErrors
	})

	samples := make(chan k6metrics.SampleContainer, 4)
	state := &lib.State{
		Samples: samples,
		Tags:    lib.NewVUStateTags(registry.RootTagSet()),
	}
	vu := fakeVU{state: state}
	(&UpdateResult{Rows: 1, LatencyMs: 2.5}).Emit(context.Background(), vu, "custom")
	(&UpdateResult{LatencyMs: 15, Error: "timeout"}).Emit(context.Background(), vu, "custom")

	counts := make(map[string]int)
	for range 4 {
		for _, sample := range (<-samples).GetSamples() {
			counts[sample.Metric.Name]++
		}
	}
	if counts["update_duration_test"] != 2 || counts["update_docs_test"] != 1 || counts["update_errors_test"] != 1 {
		t.Fatalf("emitted update metrics = %v", counts)
	}
}
