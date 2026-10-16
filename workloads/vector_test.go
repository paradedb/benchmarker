package workloads_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/workloads"
)

func TestVectorWorkloadsPreserveSourceSpecializations(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		methods, prefixes, keys []string
	}{
		{"vectorsearch", []string{"prism"}, nil, []string{"vectorsearch.version", "prism"}},
		{"vectorchord", []string{"vchordrq", "vchordg"}, []string{"vchord", "vchordrq", "vchordg"}, []string{"vectorchord"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, err := workloads.Builtin(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if w.Backend != "postgres" || !reflect.DeepEqual(w.Extras.IndexAccessMethods, tc.methods) || !reflect.DeepEqual(w.Extras.GUCPrefixes, tc.prefixes) {
				t.Fatalf("source specialization changed: %+v", w)
			}
			keys := make([]string, 0, len(w.Extras.Queries))
			for _, q := range w.Extras.Queries {
				keys = append(keys, q.Key)
			}
			if !reflect.DeepEqual(keys, tc.keys) {
				t.Fatalf("capture key/order changed: %v", keys)
			}
			if err := w.CheckBackend("postgres"); err != nil {
				t.Fatal(err)
			}
			// Workloads use PostgreSQL schema validation without their own registration.
			if _, registered := backends.GetConfig(tc.name); registered {
				t.Fatal("vector workload must not register a separate backend")
			}
			l, err := w.CLILoader(w.Connection)
			if err != nil {
				t.Fatal(err)
			}
			schema := &backends.Schema{Table: "cohere_wiki", Columns: map[string]string{"_id": "text", "emb": "vector(1024)"}}
			if l.Name() != tc.name {
				t.Fatalf("identity changed: %s", l.Name())
			}
			if err := l.ValidateSchema(schema); err != nil {
				t.Fatal(err)
			}
		})
	}
	w, _ := workloads.Builtin("vectorsearch")
	if strings.TrimSpace(w.Extras.Queries[0].SQL) != "SELECT pg_vectorsearch_version()" || !strings.Contains(w.Extras.Queries[1].SQL, "WHERE name LIKE 'prism.%'") {
		t.Fatalf("PRISM must load its library before capturing GUCs: %+v", w.Extras.Queries)
	}
}

func TestCohereWorkloadOverridesRetainScriptDefaults(t *testing.T) {
	dataset := filepath.Join("..", "datasets", "cohere-1m")
	for _, tc := range []struct{ name, want string }{
		{"paradedb", "postgres://postgres:postgres@localhost:5432/benchmark_1m"},
		{"vectorsearch", "postgres://postgres:postgres@localhost:5434/benchmark"},
		{"vectorchord", "postgres://postgres:postgres@localhost:5435/benchmark?options=-c%20vchordrq.probes%3D42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, err := workloads.Resolve(tc.name, dataset, "")
			if err != nil {
				t.Fatal(err)
			}
			if w.Connection != tc.want {
				t.Fatalf("dataset connection = %q, want %q", w.Connection, tc.want)
			}
			builtin, _ := workloads.Builtin(tc.name)
			if !reflect.DeepEqual(w.Extras, builtin.Extras) {
				t.Fatal("dataset override lost capture/telemetry")
			}
			t.Setenv(w.EnvironmentVariable(), "explicit-environment")
			if got, err := w.ResolveConnection(""); err != nil || got != "explicit-environment" {
				t.Fatalf("env override: %s, %v", got, err)
			}
		})
	}
}
