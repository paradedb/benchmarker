package metrics

import (
	"errors"
	"fmt"
	"testing"
)

func TestBaselineTelemetrySubtractsCountersAndPreservesGauges(t *testing.T) {
	labels := map[string]string{"context": "normal"}
	baseline := []TelemetryPoint{
		{Name: "index_io.read_bytes", Kind: TelemetryCounter, Value: 100, Labels: labels},
		{Name: "postgres.activity.sessions", Kind: TelemetryGauge, Value: 2},
	}
	current := []TelemetryPoint{
		{Name: "index_io.read_bytes", Kind: TelemetryCounter, Value: 175, Labels: labels},
		{Name: "postgres.activity.sessions", Kind: TelemetryGauge, Value: 4},
	}

	got := BaselineTelemetry(current, baseline)
	if got[0].Value != 75 {
		t.Fatalf("counter = %v, want 75", got[0].Value)
	}
	if got[1].Value != 4 {
		t.Fatalf("gauge = %v, want 4", got[1].Value)
	}
	got[0].Labels["context"] = "changed"
	if current[0].Labels["context"] != "normal" {
		t.Fatal("result labels alias the input")
	}
}

func TestBaselineTelemetryClampsResetCounter(t *testing.T) {
	got := BaselineTelemetry(
		[]TelemetryPoint{{Name: "wal.bytes", Kind: TelemetryCounter, Value: 10}},
		[]TelemetryPoint{{Name: "wal.bytes", Kind: TelemetryCounter, Value: 20}},
	)
	if got[0].Value != 0 {
		t.Fatalf("reset counter = %v, want 0", got[0].Value)
	}
}

func TestTelemetryErrorsAreDeduplicatedAndBounded(t *testing.T) {
	window := &BackendTelemetryWindow{}
	window.addError(errors.New("duplicate"))
	window.addError(errors.New("duplicate"))
	for i := 0; i < maxTelemetryErrorsPerWindow+5; i++ {
		window.addError(fmt.Errorf("error %d", i))
	}
	if len(window.Errors) != maxTelemetryErrorsPerWindow {
		t.Fatalf("stored errors = %d, want cap %d", len(window.Errors), maxTelemetryErrorsPerWindow)
	}
	if window.Errors[0] != "duplicate" {
		t.Fatalf("first unique error = %q, want duplicate", window.Errors[0])
	}
}
