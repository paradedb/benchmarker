package search

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/metrics"
)

const (
	telemetryPollInterval = time.Second
	telemetryQueryTimeout = 5 * time.Second
	telemetryFinalGrace   = time.Second
)

type telemetryWindow struct {
	id         string
	backend    string
	start      time.Duration
	end        time.Duration
	baselineAt time.Duration

	baseline      []metrics.TelemetryPoint
	baselineErr   error
	baselineReady bool
	started       bool
	finished      bool
	lastSample    time.Time
}

type backendTelemetryCollector struct {
	providers map[string]backends.TelemetryProvider
	windows   []*telemetryWindow
	startedAt time.Time
}

type cachedTelemetryBaseline struct {
	once   sync.Once
	points []metrics.TelemetryPoint
	err    error
}

var initialTelemetryBaselines sync.Map

func newBackendTelemetryCollector(
	scenarios map[string]interface{},
	providers map[string]backends.TelemetryProvider,
	backendCount int,
	totalDuration time.Duration,
) (*backendTelemetryCollector, error) {
	if len(providers) == 0 {
		return nil, nil
	}

	windows, err := telemetryWindowsFromScenarios(scenarios, providers)
	if err != nil {
		return nil, err
	}
	if len(windows) == 0 && !hasTaggedTelemetryWorkload(scenarios, providers) && backendCount == 1 && len(providers) == 1 && totalDuration > 0 {
		for backend := range providers {
			windows = []*telemetryWindow{{
				id: backend + "@0", backend: backend, end: totalDuration,
			}}
		}
	}
	if len(windows) == 0 {
		return nil, nil
	}

	setTelemetryBaselineTimes(windows)
	collector := &backendTelemetryCollector{providers: providers, windows: windows}
	for _, window := range windows {
		if window.start != 0 {
			continue
		}
		cache := initialTelemetryBaseline(window.id, providers[window.backend])
		window.baseline = cloneTelemetryPoints(cache.points)
		window.baselineErr = cache.err
		window.baselineReady = true
	}
	return collector, nil
}

func telemetryWindowsFromScenarios(
	scenarios map[string]interface{},
	providers map[string]backends.TelemetryProvider,
) ([]*telemetryWindow, error) {
	var windows []*telemetryWindow
	for name, raw := range scenarios {
		config, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		backend := scenarioBackend(config)
		if backend == "" {
			continue
		}
		if _, ok := providers[backend]; !ok {
			continue
		}
		if scenarioIsRamp(config) {
			continue
		}
		start, err := optionalDuration(config["startTime"])
		if err != nil {
			return nil, fmt.Errorf("scenario %s startTime: %w", name, err)
		}
		duration, err := scenarioDuration(config)
		if err != nil {
			return nil, fmt.Errorf("scenario %s: %w", name, err)
		}
		if duration <= 0 {
			continue
		}
		windows = append(windows, &telemetryWindow{
			backend: backend,
			start:   start,
			end:     start + duration,
		})
	}
	if len(windows) == 0 {
		return nil, nil
	}

	sort.Slice(windows, func(i, j int) bool {
		if windows[i].backend != windows[j].backend {
			return windows[i].backend < windows[j].backend
		}
		return windows[i].start < windows[j].start
	})

	merged := make([]*telemetryWindow, 0, len(windows))
	for _, window := range windows {
		if len(merged) > 0 {
			previous := merged[len(merged)-1]
			if previous.backend == window.backend && window.start <= previous.end {
				if window.end > previous.end {
					previous.end = window.end
				}
				continue
			}
		}
		merged = append(merged, window)
	}
	for _, window := range merged {
		window.id = fmt.Sprintf("%s@%s", window.backend, window.start)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].start == merged[j].start {
			return merged[i].backend < merged[j].backend
		}
		return merged[i].start < merged[j].start
	})
	return merged, nil
}

func hasTaggedTelemetryWorkload(scenarios map[string]interface{}, providers map[string]backends.TelemetryProvider) bool {
	for _, raw := range scenarios {
		config, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if _, ok := providers[scenarioBackend(config)]; ok {
			return true
		}
	}
	return false
}

func setTelemetryBaselineTimes(windows []*telemetryWindow) {
	for _, window := range windows {
		if window.start == 0 {
			continue
		}
		previousEnd := time.Duration(0)
		for _, candidate := range windows {
			if candidate.end <= window.start && candidate.end > previousEnd {
				previousEnd = candidate.end
			}
		}
		window.baselineAt = window.start - telemetryFinalGrace
		if window.baselineAt < previousEnd {
			window.baselineAt = previousEnd
		}
	}
}

func scenarioBackend(config map[string]interface{}) string {
	tags, ok := config["tags"].(map[string]interface{})
	if !ok {
		return ""
	}
	backend, _ := tags["backend"].(string)
	return backend
}

func scenarioIsRamp(config map[string]interface{}) bool {
	tags, ok := config["tags"].(map[string]interface{})
	if !ok {
		return false
	}
	ramp, _ := tags["ramp"].(string)
	return ramp == "true"
}

func scenarioDuration(config map[string]interface{}) (time.Duration, error) {
	if raw, ok := config["duration"]; ok {
		return optionalDuration(raw)
	}
	if raw, ok := config["stages"]; ok {
		stages, ok := raw.([]interface{})
		if !ok {
			return 0, fmt.Errorf("stages must be an array")
		}
		var total time.Duration
		for _, rawStage := range stages {
			stage, ok := rawStage.(map[string]interface{})
			if !ok {
				return 0, fmt.Errorf("stage must be an object")
			}
			duration, err := optionalDuration(stage["duration"])
			if err != nil {
				return 0, err
			}
			total += duration
		}
		return total, nil
	}
	if raw, ok := config["maxDuration"]; ok {
		return optionalDuration(raw)
	}
	return 0, nil
}

func optionalDuration(raw interface{}) (time.Duration, error) {
	if raw == nil {
		return 0, nil
	}
	text, ok := raw.(string)
	if !ok {
		return 0, fmt.Errorf("duration must be a string")
	}
	duration, err := time.ParseDuration(text)
	if err != nil || duration < 0 {
		return 0, fmt.Errorf("invalid duration %q", text)
	}
	return duration, nil
}

func initialTelemetryBaseline(id string, provider backends.TelemetryProvider) *cachedTelemetryBaseline {
	value, _ := initialTelemetryBaselines.LoadOrStore(id, &cachedTelemetryBaseline{})
	cache := value.(*cachedTelemetryBaseline)
	cache.once.Do(func() {
		cache.points, cache.err = readTelemetry(provider)
	})
	return cache
}

func readTelemetry(provider backends.TelemetryProvider) ([]metrics.TelemetryPoint, error) {
	ctx, cancel := context.WithTimeout(context.Background(), telemetryQueryTimeout)
	defer cancel()
	return provider.ReadTelemetry(ctx)
}

func (c *backendTelemetryCollector) collect(now time.Time) {
	if c == nil {
		return
	}
	if c.startedAt.IsZero() {
		c.startedAt = now
	}
	elapsed := now.Sub(c.startedAt)
	for _, window := range c.windows {
		provider := c.providers[window.backend]
		if !window.baselineReady && elapsed >= window.baselineAt {
			window.baseline, window.baselineErr = readTelemetry(provider)
			window.baselineReady = true
		}
		if !window.started && elapsed >= window.start {
			if !window.baselineReady {
				window.baseline, window.baselineErr = readTelemetry(provider)
				window.baselineReady = true
			}
			metrics.BeginBackendTelemetry(
				window.id,
				window.backend,
				c.startedAt.Add(window.start).UnixMilli(),
				errorSlice(window.baselineErr),
			)
			window.started = true
		}
		if !window.started || window.finished {
			continue
		}
		if elapsed >= window.end {
			c.sample(window, provider, now)
			metrics.FinishBackendTelemetry(window.id, c.startedAt.Add(window.end).UnixMilli())
			window.finished = true
			continue
		}
		if window.lastSample.IsZero() || now.Sub(window.lastSample) >= telemetryPollInterval {
			c.sample(window, provider, now)
		}
	}
}

func (c *backendTelemetryCollector) sample(
	window *telemetryWindow,
	provider backends.TelemetryProvider,
	now time.Time,
) {
	current, err := readTelemetry(provider)
	values := metrics.BaselineTelemetry(current, window.baseline)
	metrics.RegisterBackendTelemetry(window.id, metrics.TelemetrySample{
		Time: now.UnixMilli(), Values: values,
	}, errorSlice(err))
	window.lastSample = now
}

func errorSlice(err error) []error {
	if err == nil {
		return nil
	}
	return []error{err}
}

func cloneTelemetryPoints(points []metrics.TelemetryPoint) []metrics.TelemetryPoint {
	clone := make([]metrics.TelemetryPoint, len(points))
	for i, point := range points {
		clone[i] = point
		if point.Labels != nil {
			clone[i].Labels = make(map[string]string, len(point.Labels))
			for key, value := range point.Labels {
				clone[i].Labels[key] = value
			}
		}
	}
	return clone
}
