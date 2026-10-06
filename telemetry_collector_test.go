package search

import (
	"testing"
	"time"

	"github.com/paradedb/benchmarker/backends"
)

func TestTelemetryWindowsFollowTaggedNativeScenarios(t *testing.T) {
	scenarios := map[string]interface{}{
		"backend_a_query": map[string]interface{}{
			"duration": "30s",
			"tags":     map[string]interface{}{"backend": "backend-a"},
		},
		"backend_a_updates": map[string]interface{}{
			"duration": "20s",
			"tags":     map[string]interface{}{"backend": "backend-a"},
		},
		"backend_a_late_overlap": map[string]interface{}{
			"startTime": "10s",
			"duration":  "30s",
			"tags":      map[string]interface{}{"backend": "backend-a"},
		},
		"backend_b_ramp": map[string]interface{}{
			"startTime": "35s",
			"stages": []interface{}{
				map[string]interface{}{"duration": "10s", "target": float64(5)},
				map[string]interface{}{"duration": "20s", "target": float64(5)},
			},
			"tags": map[string]interface{}{"backend": "backend-b"},
		},
		"unrelated": map[string]interface{}{
			"duration": "10s",
			"tags":     map[string]interface{}{"backend": "elasticsearch"},
		},
	}
	providers := map[string]backends.TelemetryProvider{
		"backend-a": nil,
		"backend-b": nil,
	}

	windows, err := telemetryWindowsFromScenarios(scenarios, providers)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 2 {
		t.Fatalf("got %d windows, want 2", len(windows))
	}
	byBackend := make(map[string]*telemetryWindow, len(windows))
	for _, window := range windows {
		byBackend[window.backend] = window
	}
	backendA := byBackend["backend-a"]
	if backendA == nil || backendA.start != 0 || backendA.end != 40*time.Second {
		t.Fatalf("merged backend-a interval = %#v, want start 0s and end 40s", backendA)
	}
	backendB := byBackend["backend-b"]
	if backendB == nil || backendB.start != 35*time.Second || backendB.end != 65*time.Second {
		t.Fatalf("backend-b interval = %#v, want start 35s and end 65s", backendB)
	}

	setTelemetryBaselineTimes(windows)
	if backendB.baselineAt != 34*time.Second {
		t.Fatalf("backend-b baseline = %s, want 34s", backendB.baselineAt)
	}
}

func TestScenarioDurationUsesMaxDurationForIterationExecutors(t *testing.T) {
	duration, err := scenarioDuration(map[string]interface{}{"maxDuration": "45s"})
	if err != nil {
		t.Fatal(err)
	}
	if duration != 45*time.Second {
		t.Fatalf("duration = %s, want 45s", duration)
	}
}

func TestTelemetryWindowsExcludeExplicitRampWorkloads(t *testing.T) {
	scenarios := map[string]interface{}{
		"warm_search": map[string]interface{}{
			"duration": "30s",
			"tags": map[string]interface{}{
				"backend": "paradedb",
				"ramp":    "true",
			},
		},
		"measured_search": map[string]interface{}{
			"startTime": "30s",
			"duration":  "60s",
			"tags":      map[string]interface{}{"backend": "paradedb"},
		},
	}
	providers := map[string]backends.TelemetryProvider{"paradedb": nil}

	windows, err := telemetryWindowsFromScenarios(scenarios, providers)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 {
		t.Fatalf("got %d telemetry windows, want only the measured workload", len(windows))
	}
	if windows[0].start != 30*time.Second || windows[0].end != 90*time.Second {
		t.Fatalf("measured telemetry window = %#v, want 30s..90s", windows[0])
	}
}

func TestExplicitRampDoesNotEnableFallbackTelemetryWindow(t *testing.T) {
	scenarios := map[string]interface{}{
		"warm_search": map[string]interface{}{
			"duration": "30s",
			"tags": map[string]interface{}{
				"backend": "paradedb",
				"ramp":    "true",
			},
		},
	}
	providers := map[string]backends.TelemetryProvider{"paradedb": nil}

	collector, err := newBackendTelemetryCollector(scenarios, providers, 1, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if collector != nil {
		t.Fatalf("ramp-only workload created telemetry collector: %#v", collector)
	}
}
