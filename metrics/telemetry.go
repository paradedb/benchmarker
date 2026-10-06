package metrics

import (
	"sort"
	"strings"
	"sync"
)

// TelemetryKind controls how a backend-provided series is measured. Counters
// are reported relative to the start of a workload window; gauges are reported
// as their current value.
type TelemetryKind string

const (
	TelemetryCounter TelemetryKind = "counter"
	TelemetryGauge   TelemetryKind = "gauge"
)

// TelemetryPoint is one named value returned by a backend telemetry provider.
// Labels distinguish series such as pg_stat_io rows.
type TelemetryPoint struct {
	Name   string            `json:"name"`
	Kind   TelemetryKind     `json:"kind"`
	Unit   string            `json:"unit,omitempty"`
	Value  float64           `json:"value"`
	Labels map[string]string `json:"labels,omitempty"`
}

type TelemetrySample struct {
	Time   int64            `json:"time"`
	Values []TelemetryPoint `json:"values"`
}

// BackendTelemetryWindow contains baseline-relative telemetry for one native
// k6 workload window.
type BackendTelemetryWindow struct {
	Backend    string            `json:"backend"`
	StartTime  int64             `json:"startTime"`
	EndTime    int64             `json:"endTime,omitempty"`
	Samples    []TelemetrySample `json:"samples"`
	Errors     []string          `json:"errors,omitempty"`
	seenErrors map[string]struct{}
}

const maxTelemetryErrorsPerWindow = 10

var backendTelemetry = struct {
	sync.RWMutex
	windows map[string]*BackendTelemetryWindow
}{windows: make(map[string]*BackendTelemetryWindow)}

// BeginBackendTelemetry opens a workload window. The baseline remains in the
// collector; only baseline-relative samples are retained here.
func BeginBackendTelemetry(id, backend string, startTime int64, errors []error) {
	backendTelemetry.Lock()
	defer backendTelemetry.Unlock()
	window := &BackendTelemetryWindow{Backend: backend, StartTime: startTime}
	for _, err := range errors {
		window.addError(err)
	}
	backendTelemetry.windows[id] = window
}

func RegisterBackendTelemetry(id string, sample TelemetrySample, errors []error) {
	backendTelemetry.Lock()
	defer backendTelemetry.Unlock()
	window := backendTelemetry.windows[id]
	if window == nil {
		return
	}
	for _, err := range errors {
		window.addError(err)
	}
	if len(sample.Values) > 0 {
		window.Samples = append(window.Samples, cloneTelemetrySample(sample))
	}
}

func FinishBackendTelemetry(id string, endTime int64) {
	backendTelemetry.Lock()
	defer backendTelemetry.Unlock()
	if window := backendTelemetry.windows[id]; window != nil {
		window.EndTime = endTime
	}
}

func GetBackendTelemetry() map[string][]BackendTelemetryWindow {
	backendTelemetry.RLock()
	defer backendTelemetry.RUnlock()
	result := make(map[string][]BackendTelemetryWindow)
	for _, window := range backendTelemetry.windows {
		copy := *window
		copy.Samples = make([]TelemetrySample, len(window.Samples))
		for i, sample := range window.Samples {
			copy.Samples[i] = cloneTelemetrySample(sample)
		}
		copy.Errors = append([]string(nil), window.Errors...)
		result[window.Backend] = append(result[window.Backend], copy)
	}
	for backend := range result {
		sort.Slice(result[backend], func(i, j int) bool {
			return result[backend][i].StartTime < result[backend][j].StartTime
		})
	}
	return result
}

// BaselineTelemetry applies the generic counter/gauge semantics to a snapshot.
func BaselineTelemetry(current, baseline []TelemetryPoint) []TelemetryPoint {
	baselineByKey := make(map[string]TelemetryPoint, len(baseline))
	for _, point := range baseline {
		baselineByKey[telemetryPointKey(point)] = point
	}
	result := make([]TelemetryPoint, 0, len(current))
	for _, point := range current {
		value := point.Value
		if point.Kind == TelemetryCounter {
			if initial, ok := baselineByKey[telemetryPointKey(point)]; ok {
				value -= initial.Value
				if value < 0 {
					value = 0
				}
			}
		}
		point.Value = value
		point.Labels = cloneLabels(point.Labels)
		result = append(result, point)
	}
	return result
}

func telemetryPointKey(point TelemetryPoint) string {
	keys := make([]string, 0, len(point.Labels))
	for key := range point.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(point.Name)
	for _, key := range keys {
		builder.WriteByte('\x00')
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(point.Labels[key])
	}
	return builder.String()
}

func cloneTelemetrySample(sample TelemetrySample) TelemetrySample {
	clone := sample
	clone.Values = make([]TelemetryPoint, len(sample.Values))
	for i, point := range sample.Values {
		clone.Values[i] = point
		clone.Values[i].Labels = cloneLabels(point.Labels)
	}
	return clone
}

func cloneLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	clone := make(map[string]string, len(labels))
	for key, value := range labels {
		clone[key] = value
	}
	return clone
}

func (w *BackendTelemetryWindow) addError(err error) {
	if err == nil {
		return
	}
	message := err.Error()
	if w.seenErrors == nil {
		w.seenErrors = make(map[string]struct{})
	}
	if _, exists := w.seenErrors[message]; exists {
		return
	}
	w.seenErrors[message] = struct{}{}
	if len(w.Errors) < maxTelemetryErrorsPerWindow {
		w.Errors = append(w.Errors, message)
	}
}
