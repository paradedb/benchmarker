package workloads_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/paradedb/benchmarker/backends"
	pg "github.com/paradedb/benchmarker/backends/shared/postgres"
	"github.com/paradedb/benchmarker/metrics"
	"github.com/paradedb/benchmarker/workloads"
)

// Compare against the original uncommitted factories, independently of YAML.
// The supplied databases must already have their extensions installed.
func TestLiveVectorWorkloadCaptureParity(t *testing.T) {
	for _, tc := range []struct {
		name, env, section, key string
		prefixes, methods       []string
		queries                 []pg.ConfigQuery
	}{
		{
			name: "vectorsearch", env: "BENCHMARKER_VECTORSEARCH_URL", section: "prism", key: "prism.nprobe",
			methods: []string{"prism"},
			queries: []pg.ConfigQuery{
				{Key: "vectorsearch.version", Query: "SELECT pg_vectorsearch_version()"},
				{Key: "prism", Query: "SELECT name, setting || COALESCE(unit, '') FROM pg_settings WHERE name LIKE 'prism.%' ORDER BY name"},
			},
		},
		{
			name: "vectorchord", env: "BENCHMARKER_VECTORCHORD_VECTOR_URL", section: "vectorchord", key: "vectorchord.vchord_version",
			prefixes: []string{"vchord", "vchordrq", "vchordg"}, methods: []string{"vchordrq", "vchordg"},
			queries: []pg.ConfigQuery{{Key: "vectorchord", Query: "SELECT 'vectorchord.' || extname || '_version', extversion FROM pg_extension WHERE extname IN ('vchord', 'vector') ORDER BY extname"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := os.Getenv(tc.env)
			if conn == "" {
				t.Skip("set " + tc.env + " for live parity checks")
			}
			old, err := pg.New(conn)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			baseline := old.(*pg.Driver)
			baseline.SetExtraGUCPrefixes(tc.prefixes)
			baseline.SetExtraQueries(tc.queries)
			telemetry := pg.StandardTelemetryQueries()
			telemetry = append(telemetry, pg.IndexIOTelemetryQueries(tc.methods...)...)
			baseline.SetTelemetryQueries(telemetry...)
			baseline.CaptureConfig(context.Background(), tc.name+"-original")
			want := metrics.GetBackendConfig(tc.name + "-original")
			section, ok := want[tc.section].(map[string]string)
			if !ok || section[tc.key] == "" {
				t.Fatalf("extension capture unavailable: %+v", want)
			}
			w, err := workloads.Builtin(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			d, err := w.NewDriver(conn)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			alias := tc.name + "-workload"
			d.CaptureConfig(context.Background(), alias)
			got := metrics.GetBackendConfig(alias)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("capture changed from source factory\ngot: %+v\nwant: %+v", got, want)
			}
			identities := func(provider backends.TelemetryProvider) map[string]bool {
				points, err := provider.ReadTelemetry(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if len(points) == 0 {
					t.Fatal("missing telemetry")
				}
				result := map[string]bool{}
				for _, p := range points {
					labels, err := json.Marshal(p.Labels)
					if err != nil {
						t.Fatal(err)
					}
					result[p.Name+":"+string(p.Kind)+":"+p.Unit+":"+string(labels)] = true
				}
				return result
			}
			if !reflect.DeepEqual(identities(baseline), identities(d.(backends.TelemetryProvider))) {
				t.Fatal("telemetry identities changed from source factory")
			}
		})
	}
}
