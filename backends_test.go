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
