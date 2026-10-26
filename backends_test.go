package search

import (
	"testing"

	"github.com/paradedb/benchmarker/metrics"
)

func TestTurbopufferAliasesHaveNoDockerCollector(t *testing.T) {
	t.Setenv("TURBOPUFFER_API_KEY", "test-secret")
	t.Setenv("TURBOPUFFER_REGION", "aws-us-east-1")
	t.Setenv("TURBOPUFFER_URL", "")
	m := &ModuleInstance{}
	clients := m.newBackends(map[string]interface{}{
		"datasetPath": t.TempDir(),
		"backends":    []interface{}{"turbopuffer", map[string]interface{}{"type": "turbopuffer", "alias": "tpuf-cloud"}},
	})
	defer clients.Close()
	if clients.Metrics != nil {
		t.Fatal("cloud backends created Docker collector")
	}
	for _, name := range []string{"turbopuffer", "tpuf-cloud"} {
		if clients.Get(name) == nil {
			t.Fatalf("missing client %s", name)
		}
		if opts := metrics.GetBackendOptions(name); opts == nil || opts.Container != "" {
			t.Fatalf("unexpected container for %s: %+v", name, opts)
		}
	}
}

func TestTINAliasesHaveNoDockerCollector(t *testing.T) {
	fakeBackend(t, "postgres")
	t.Setenv("TIN_URL", "postgres://remote/benchmark")
	clients := (&ModuleInstance{}).newBackends(map[string]interface{}{
		"datasetPath": t.TempDir(),
		"backends":    []interface{}{"tin", map[string]interface{}{"type": "tin", "alias": "tin-remote"}},
	})
	defer clients.Close()
	if clients.Metrics != nil {
		t.Fatal("TIN created a Docker collector")
	}
	for _, name := range []string{"tin", "tin-remote"} {
		if clients.Get(name) == nil {
			t.Fatalf("missing client %s", name)
		}
		if opts := metrics.GetBackendOptions(name); opts == nil || opts.Container != "" {
			t.Fatalf("unexpected container for %s: %+v", name, opts)
		}
		if clients.telemetryProviders[name] == nil {
			t.Fatalf("database telemetry disabled for %s", name)
		}
		if got := metrics.GetBackendConfig(name)["connection_used"]; got != "postgres://remote/benchmark" {
			t.Fatalf("connection = %v", got)
		}
	}
}
