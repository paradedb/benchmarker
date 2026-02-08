package metrics

import (
	"context"
	"sync"
	"time"

	"go.k6.io/k6/js/modules"
	"go.k6.io/k6/metrics"
)

var (
	// Unified metrics - tagged by backend
	searchDuration *metrics.Metric
	searchHits     *metrics.Metric
	ingestDuration *metrics.Metric
	ingestDocs     *metrics.Metric
	metricsRegOnce sync.Once

	// Query patterns per scenario (captured on first call)
	QueryPatterns   = make(map[string]string)
	queryPatternsMu sync.RWMutex
)

// RegisterMetrics registers the unified metrics once during init phase.
// Call this from any backend's NewClient during init.
func RegisterMetrics(vu modules.VU) {
	metricsRegOnce.Do(func() {
		if initEnv := vu.InitEnv(); initEnv != nil {
			registry := initEnv.Registry
			searchDuration, _ = registry.NewMetric("search_duration", metrics.Trend, metrics.Time)
			searchHits, _ = registry.NewMetric("search_hits", metrics.Gauge)
			ingestDuration, _ = registry.NewMetric("ingest_duration", metrics.Trend, metrics.Time)
			ingestDocs, _ = registry.NewMetric("ingest_docs", metrics.Counter)
		}
	})
}

// CaptureQueryPattern stores the first query pattern seen for each scenario.
func CaptureQueryPattern(vu modules.VU, query string) {
	state := vu.State()
	if state == nil {
		return
	}

	tags := state.Tags.GetCurrentValues()
	scenario, ok := tags.Tags.Get("scenario")
	if !ok {
		return
	}

	queryPatternsMu.RLock()
	_, exists := QueryPatterns[scenario]
	queryPatternsMu.RUnlock()

	if !exists {
		queryPatternsMu.Lock()
		if _, exists := QueryPatterns[scenario]; !exists {
			QueryPatterns[scenario] = query
		}
		queryPatternsMu.Unlock()
	}
}

// SearchResult represents the result of a search operation.
type SearchResult struct {
	Hits      int64
	LatencyMs float64
	Error     string
}

// EmitOptions contains optional tags for metric emission.
type EmitOptions struct {
	Container string
	Alias     string
}

// Emit pushes search metrics to k6 with the backend tag.
func (r *SearchResult) Emit(ctx context.Context, vu modules.VU, backend string, opts ...EmitOptions) {
	if r.Error != "" {
		return // Don't emit metrics on error
	}

	state := vu.State()
	if state == nil || searchDuration == nil || searchHits == nil {
		return
	}

	now := time.Now()
	tags := state.Tags.GetCurrentValues().Tags
	if backend != "" {
		tags = tags.With("backend", backend)
	}

	// Add container and alias tags if provided
	if len(opts) > 0 {
		if opts[0].Container != "" {
			tags = tags.With("container", opts[0].Container)
		}
		if opts[0].Alias != "" {
			tags = tags.With("alias", opts[0].Alias)
		}
	}

	metrics.PushIfNotDone(ctx, state.Samples, metrics.Sample{
		TimeSeries: metrics.TimeSeries{Metric: searchDuration, Tags: tags},
		Time:       now,
		Value:      r.LatencyMs,
	})
	metrics.PushIfNotDone(ctx, state.Samples, metrics.Sample{
		TimeSeries: metrics.TimeSeries{Metric: searchHits, Tags: tags},
		Time:       now,
		Value:      float64(r.Hits),
	})
}

// ToMap converts the result to a map for JavaScript.
func (r *SearchResult) ToMap() map[string]interface{} {
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
func (r *IngestResult) Emit(ctx context.Context, vu modules.VU, backend string, opts ...EmitOptions) {
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

	// Add container and alias tags if provided
	if len(opts) > 0 {
		if opts[0].Container != "" {
			tags = tags.With("container", opts[0].Container)
		}
		if opts[0].Alias != "" {
			tags = tags.With("alias", opts[0].Alias)
		}
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
