package metrics

import (
	"context"
	"sync"
	"time"

	"go.k6.io/k6/js/modules"
	"go.k6.io/k6/lib"
	"go.k6.io/k6/lib/executor"
	"go.k6.io/k6/metrics"
)

var (
	// Unified metrics - tagged by backend
	queryDuration  *metrics.Metric
	queryHits      *metrics.Metric
	ingestDuration  *metrics.Metric
	ingestDocs      *metrics.Metric
	updateDuration  *metrics.Metric
	updateDocs      *metrics.Metric
	backendInit     *metrics.Metric
	scenarioStarted *metrics.Metric
	metricsRegOnce  sync.Once

	// Query patterns per backend/chart/scenario (captured on first call)
	QueryPatterns   = make(map[string]string)
	queryPatternsMu sync.RWMutex

	// Scenario info per scenario (captured on first call)
	ScenarioInfos   = make(map[string]*ScenarioInfo)
	scenarioInfosMu sync.RWMutex
)

// ScenarioInfo holds executor and VU information for a scenario.
type ScenarioInfo struct {
	Executor string
	VUs      int64
}

func queryPatternKey(backend, chart, scenario string) string {
	return backend + "\x00" + chart + "\x00" + scenario
}

// GetQueryPattern returns a query pattern by backend/chart/scenario.
func GetQueryPattern(backend, chart, scenario string) string {
	queryPatternsMu.RLock()
	defer queryPatternsMu.RUnlock()

	// Prefer exact key (backend + chart + scenario).
	if q, ok := QueryPatterns[queryPatternKey(backend, chart, scenario)]; ok {
		return q
	}
	// Backward-compatibility: older runs only keyed by scenario.
	return QueryPatterns[scenario]
}

// GetScenarioInfo returns scenario metadata by name.
func GetScenarioInfo(scenario string) *ScenarioInfo {
	scenarioInfosMu.RLock()
	defer scenarioInfosMu.RUnlock()
	info := ScenarioInfos[scenario]
	if info == nil {
		return nil
	}
	copy := *info
	return &copy
}

// RegisterMetrics registers the unified metrics once during init phase.
// Call this from any backend's NewClient during init.
func RegisterMetrics(vu modules.VU) {
	if vu == nil || vu.InitEnv() == nil {
		return
	}
	metricsRegOnce.Do(func() {
		registry := vu.InitEnv().Registry
		queryDuration, _ = registry.NewMetric("query_duration", metrics.Trend, metrics.Time)
		queryHits, _ = registry.NewMetric("query_hits", metrics.Gauge)
		ingestDuration, _ = registry.NewMetric("ingest_duration", metrics.Trend, metrics.Time)
		ingestDocs, _ = registry.NewMetric("ingest_docs", metrics.Counter)
		updateDuration, _ = registry.NewMetric("update_duration", metrics.Trend, metrics.Time)
		updateDocs, _ = registry.NewMetric("update_docs", metrics.Counter)
		backendInit, _ = registry.NewMetric("backend_init", metrics.Gauge)
		scenarioStarted, _ = registry.NewMetric("scenario_started", metrics.Gauge)
	})
}

// emitGaugeMetric is a shared helper for emitting gauge metrics with backend tags.
func emitGaugeMetric(vu modules.VU, metric *metrics.Metric, backend string) {
	state := vu.State()
	if state == nil || metric == nil {
		return
	}

	ctxPtr := vu.Context()
	if ctxPtr == nil {
		return
	}

	tags := state.Tags.GetCurrentValues().Tags
	if backend != "" {
		tags = tags.With("backend", backend)
	}

	metrics.PushIfNotDone(ctxPtr, state.Samples, metrics.Sample{
		TimeSeries: metrics.TimeSeries{Metric: metric, Tags: tags},
		Time:       time.Now(),
		Value:      1,
	})
}

// EmitBackendInit emits a backend_init metric to signal the dashboard that a backend is configured.
// This allows container metrics to attach immediately, before any queries complete.
func EmitBackendInit(vu modules.VU, backend string) {
	emitGaugeMetric(vu, backendInit, backend)
}

// EmitScenarioStarted emits a scenario_started metric to signal the dashboard that a scenario has begun.
// This creates the run entry in the dashboard before any queries complete.
func EmitScenarioStarted(vu modules.VU, backend string) {
	emitGaugeMetric(vu, scenarioStarted, backend)
}

func storeQueryPattern(backend, chart, scenario, query string) {
	if scenario == "" {
		return
	}

	key := queryPatternKey(backend, chart, scenario)

	queryPatternsMu.RLock()
	_, exists := QueryPatterns[key]
	queryPatternsMu.RUnlock()

	if exists {
		return
	}

	queryPatternsMu.Lock()
	if _, exists := QueryPatterns[key]; !exists {
		QueryPatterns[key] = query
	}
	queryPatternsMu.Unlock()
}

// CaptureQueryPattern stores the first query pattern seen for each backend/chart/scenario.
// The backend parameter is required because it is added per-sample (via tags.With) and
// is not present in the VU's default state tags.
func CaptureQueryPattern(vu modules.VU, backend, query string) {
	if vu == nil {
		return
	}

	state := vu.State()
	if state == nil || state.Tags == nil {
		return
	}

	tags := state.Tags.GetCurrentValues()
	scenario, ok := tags.Tags.Get("scenario")
	if !ok {
		return
	}
	chart, _ := tags.Tags.Get("chart")
	if backend == "" {
		backend, _ = tags.Tags.Get("backend")
	}
	storeQueryPattern(backend, chart, scenario, query)
}

// CaptureScenarioInfo stores executor and VU info for each scenario.
func CaptureScenarioInfo(vu modules.VU) {
	if vu == nil {
		return
	}

	state := vu.State()
	if state == nil || state.Tags == nil {
		return
	}

	tags := state.Tags.GetCurrentValues()
	scenario, ok := tags.Tags.Get("scenario")
	if !ok {
		return
	}

	scenarioInfosMu.RLock()
	_, exists := ScenarioInfos[scenario]
	scenarioInfosMu.RUnlock()

	if exists {
		return
	}

	// Look up scenario config from options
	scenarioConfig, ok := state.Options.Scenarios[scenario]
	if !ok {
		return
	}

	info := &ScenarioInfo{
		Executor: scenarioConfig.GetType(),
	}

	// Extract VUs based on executor type
	info.VUs = getExecutorVUs(scenarioConfig)

	scenarioInfosMu.Lock()
	if _, exists := ScenarioInfos[scenario]; !exists {
		ScenarioInfos[scenario] = info
	}
	scenarioInfosMu.Unlock()
}

// maxRampingVUs returns the maximum target VUs across all stages.
func maxRampingVUs(stages []executor.Stage) int64 {
	var maxVUs int64
	for _, stage := range stages {
		if stage.Target.Int64 > maxVUs {
			maxVUs = stage.Target.Int64
		}
	}
	return maxVUs
}

// getExecutorVUs extracts VU count from various executor config types.
func getExecutorVUs(cfg lib.ExecutorConfig) int64 {
	switch c := cfg.(type) {
	case executor.ConstantVUsConfig:
		return c.VUs.Int64
	case *executor.ConstantVUsConfig:
		return c.VUs.Int64
	case executor.RampingVUsConfig:
		return maxRampingVUs(c.Stages)
	case *executor.RampingVUsConfig:
		return maxRampingVUs(c.Stages)
	case executor.SharedIterationsConfig:
		return c.VUs.Int64
	case *executor.SharedIterationsConfig:
		return c.VUs.Int64
	case executor.PerVUIterationsConfig:
		return c.VUs.Int64
	case *executor.PerVUIterationsConfig:
		return c.VUs.Int64
	case *executor.ConstantArrivalRateConfig:
		return c.PreAllocatedVUs.Int64
	case *executor.RampingArrivalRateConfig:
		return c.PreAllocatedVUs.Int64
	}
	return 0
}

// QueryResult represents the result of a query operation.
type QueryResult struct {
	Hits      int64
	LatencyMs float64
	Error     string
}

// Emit pushes query metrics to k6 with the backend tag.
func (r *QueryResult) Emit(ctx context.Context, vu modules.VU, backend string) {
	if r.Error != "" {
		return // Don't emit metrics on error
	}

	state := vu.State()
	if state == nil || queryDuration == nil || queryHits == nil {
		return
	}

	now := time.Now()
	tags := state.Tags.GetCurrentValues().Tags
	if backend != "" {
		tags = tags.With("backend", backend)
	}

	metrics.PushIfNotDone(ctx, state.Samples, metrics.Sample{
		TimeSeries: metrics.TimeSeries{Metric: queryDuration, Tags: tags},
		Time:       now,
		Value:      r.LatencyMs,
	})
	metrics.PushIfNotDone(ctx, state.Samples, metrics.Sample{
		TimeSeries: metrics.TimeSeries{Metric: queryHits, Tags: tags},
		Time:       now,
		Value:      float64(r.Hits),
	})
}

// ToMap converts the result to a map for JavaScript.
func (r *QueryResult) ToMap() map[string]interface{} {
	m := map[string]interface{}{
		"hits":      r.Hits,
		"latencyMs": r.LatencyMs,
	}
	if r.Error != "" {
		m["error"] = r.Error
	}
	return m
}

// IngestResult represents the result of an insert/ingest operation.
type IngestResult struct {
	Rows      int
	LatencyMs float64
	Error     string
}

// Emit pushes ingest metrics to k6 with the backend tag.
func (r *IngestResult) Emit(ctx context.Context, vu modules.VU, backend string) {
	if r.Error != "" {
		return // Don't emit metrics on error
	}

	state := vu.State()
	if state == nil || ingestDuration == nil || ingestDocs == nil {
		return
	}

	now := time.Now()
	tags := state.Tags.GetCurrentValues().Tags
	if backend != "" {
		tags = tags.With("backend", backend)
	}

	metrics.PushIfNotDone(ctx, state.Samples, metrics.Sample{
		TimeSeries: metrics.TimeSeries{Metric: ingestDuration, Tags: tags},
		Time:       now,
		Value:      r.LatencyMs,
	})
	metrics.PushIfNotDone(ctx, state.Samples, metrics.Sample{
		TimeSeries: metrics.TimeSeries{Metric: ingestDocs, Tags: tags},
		Time:       now,
		Value:      float64(r.Rows),
	})
}

// ToMap converts the result to a map for JavaScript.
func (r *IngestResult) ToMap() map[string]interface{} {
	m := map[string]interface{}{
		"rows":      r.Rows,
		"latencyMs": r.LatencyMs,
	}
	if r.Error != "" {
		m["error"] = r.Error
	}
	return m
}

// UpdateResult represents the result of an update operation.
type UpdateResult struct {
	Rows      int
	LatencyMs float64
	Error     string
}

// Emit pushes update metrics to k6 with the backend tag.
func (r *UpdateResult) Emit(ctx context.Context, vu modules.VU, backend string) {
	if r.Error != "" {
		return
	}

	state := vu.State()
	if state == nil || updateDuration == nil || updateDocs == nil {
		return
	}

	now := time.Now()
	tags := state.Tags.GetCurrentValues().Tags
	if backend != "" {
		tags = tags.With("backend", backend)
	}

	metrics.PushIfNotDone(ctx, state.Samples, metrics.Sample{
		TimeSeries: metrics.TimeSeries{Metric: updateDuration, Tags: tags},
		Time:       now,
		Value:      r.LatencyMs,
	})
	metrics.PushIfNotDone(ctx, state.Samples, metrics.Sample{
		TimeSeries: metrics.TimeSeries{Metric: updateDocs, Tags: tags},
		Time:       now,
		Value:      float64(r.Rows),
	})
}

// ToMap converts the result to a map for JavaScript.
func (r *UpdateResult) ToMap() map[string]interface{} {
	m := map[string]interface{}{
		"rows":      r.Rows,
		"latencyMs": r.LatencyMs,
	}
	if r.Error != "" {
		m["error"] = r.Error
	}
	return m
}

// ExecResult represents the result of a non-query SQL execution.
type ExecResult struct {
	RowsAffected int64
	LatencyMs    float64
	Error        string
}

// ToMap converts the result to a map for JavaScript.
func (r *ExecResult) ToMap() map[string]interface{} {
	m := map[string]interface{}{
		"rowsAffected": r.RowsAffected,
		"latencyMs":    r.LatencyMs,
	}
	if r.Error != "" {
		m["error"] = r.Error
	}
	return m
}
