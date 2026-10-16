package workloads_test

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/backends/paradedb"
	pg "github.com/paradedb/benchmarker/backends/shared/postgres"
	"github.com/paradedb/benchmarker/metrics"
	"github.com/paradedb/benchmarker/workloads"
)

// These tests use read-only queries. Supplying the URLs opts into live checks.
func TestLiveWorkloadQueriesAndOverrides(t *testing.T) {
	conn := os.Getenv("BENCHMARKER_POSTGRES_TEST_URL")
	if conn == "" {
		t.Skip("set BENCHMARKER_POSTGRES_TEST_URL for live workload checks")
	}
	w, err := workloads.Builtin("postgres")
	if err != nil {
		t.Fatal(err)
	}
	w.Extras = workloads.Extras{
		GUCs: []string{"maintenance_work_mem"},
		Queries: []workloads.Query{
			{SQL: "SELECT 'column-value'::text AS custom_version"},
			{Key: "stable.key", SQL: "SELECT 'explicit-value'::text AS ignored_column_name"},
			{Key: "rows", SQL: "SELECT 'custom.first'::text, 'one'::text UNION ALL SELECT 'custom.second', 'two'"},
		},
		IndexAccessMethods: []string{},
	}
	d, err := w.NewDriver(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.CaptureConfig(context.Background(), "workload-query-test")
	got := metrics.GetBackendConfig("workload-query-test")
	if got["custom_version"] != "column-value" || got["stable.key"] != "explicit-value" {
		t.Fatalf("query keys/values: %+v", got)
	}
	if !reflect.DeepEqual(got["custom"], map[string]string{"custom.first": "one", "custom.second": "two"}) {
		t.Fatalf("multirow query: %+v", got)
	}
	settings, ok := got["postgresql"].(map[string]string)
	if !ok || settings["shared_buffers"] == "" || settings["maintenance_work_mem"] == "" {
		t.Fatalf("GUC capture: %+v", got)
	}
	if hits, err := d.Query(context.Background(), "SELECT 1"); err != nil || hits != 1 {
		t.Fatalf("query: %d, %v", hits, err)
	}
	points, err := d.(backends.TelemetryProvider).ReadTelemetry(context.Background())
	// Some standard views are version-dependent; the driver preserves other series.
	if len(points) == 0 {
		t.Fatalf("no standard telemetry: %v", err)
	}
	for _, point := range points {
		if strings.HasPrefix(point.Name, "postgres.index") {
			t.Fatalf("index telemetry not disabled: %+v", point)
		}
	}
}

func TestLiveParadeDBCompatibility(t *testing.T) {
	conn := os.Getenv("BENCHMARKER_PARADEDB_TEST_URL")
	if conn == "" {
		t.Skip("set BENCHMARKER_PARADEDB_TEST_URL for live compatibility checks")
	}
	// Recreate the pre-workload factory as the independent compatibility oracle.
	old, err := pg.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	baseline := old.(*pg.Driver)
	baseline.SetExtraGUCPrefixes([]string{"paradedb"})
	baseline.SetExtraQueries([]pg.ConfigQuery{
		{Key: "paradedb.version", Query: "SELECT paradedb.version_info()::text"},
		{Key: "segments_at_run_start", Query: `SELECT 'paradedb.segments_at_run_start.' || c.relname,
   (SELECT count(*) FROM paradedb.index_info(c.oid::regclass))::text
   FROM pg_class c JOIN pg_am am ON c.relam = am.oid
   WHERE am.amname = 'paradedb' ORDER BY c.relname`},
	})
	telemetry := pg.StandardTelemetryQueries()
	telemetry = append(telemetry, pg.IndexIOTelemetryQueries("paradedb", "bm25")...)
	baseline.SetTelemetryQueries(telemetry...)
	baseline.CaptureConfig(context.Background(), "baseline-paradedb")
	want := metrics.GetBackendConfig("baseline-paradedb")
	if want["paradedb.version"] == nil || want["paradedb"] == nil {
		t.Fatalf("ParadeDB not available: %+v", want)
	}
	w, err := workloads.Builtin("paradedb")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		factory backends.DriverFactory
	}{
		{"legacy-go-api", paradedb.New}, {"explicit-workload", w.NewDriver},
		{"legacy-registry", func(conn string) (backends.Driver, error) { return backends.NewDriver("paradedb", conn) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := tc.factory(conn)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			d.CaptureConfig(context.Background(), tc.name)
			got := metrics.GetBackendConfig(tc.name)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("capture differs from old factory\ngot: %+v\nwant: %+v", got, want)
			}
			expected, _ := baseline.ReadTelemetry(context.Background())
			actual, _ := d.(backends.TelemetryProvider).ReadTelemetry(context.Background())
			keys := func(points []metrics.TelemetryPoint) map[string]bool {
				out := map[string]bool{}
				for _, p := range points {
					out[p.Name+":"+string(p.Kind)+":"+p.Unit] = true
				}
				return out
			}
			if len(actual) == 0 || !reflect.DeepEqual(keys(actual), keys(expected)) {
				t.Fatalf("telemetry series changed: %v vs %v", keys(actual), keys(expected))
			}
		})
	}
}
