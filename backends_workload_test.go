package search

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/metrics"
)

type workloadTestDriver struct{ connection string }

func (*workloadTestDriver) Close() error                                       { return nil }
func (*workloadTestDriver) Exec(context.Context, string) error                 { return nil }
func (*workloadTestDriver) Query(context.Context, string, ...any) (int, error) { return 0, nil }
func (*workloadTestDriver) Insert(context.Context, string, []string, [][]any) (int, error) {
	return 0, nil
}
func (*workloadTestDriver) Update(context.Context, string, []string, []string, [][]any) (int, error) {
	return 0, nil
}
func (d *workloadTestDriver) CaptureConfig(_ context.Context, alias string) {
	metrics.RegisterBackendConfig(alias, map[string]interface{}{"connection_used": d.connection})
}
func (*workloadTestDriver) TelemetryEnabled() bool { return true }
func (*workloadTestDriver) ReadTelemetry(context.Context) ([]metrics.TelemetryPoint, error) {
	return nil, nil
}

func fakeBackend(t *testing.T, name string) {
	t.Helper()
	original, ok := backends.GetConfig(name)
	if !ok {
		t.Fatal(name)
	}
	cfg := original
	cfg.Factory = func(conn string) (backends.Driver, error) { return &workloadTestDriver{connection: conn}, nil }
	backends.Register(name, cfg)
	t.Cleanup(func() { backends.Register(name, original) })
}

func writeWorkloadTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyBackendConfigurationsRetainDefaults(t *testing.T) {
	for _, name := range backends.RegisteredBackends() {
		t.Run(name, func(t *testing.T) {
			fakeBackend(t, name)
			cfg, _ := backends.GetConfig(name)
			t.Setenv(cfg.EnvVar, "must-not-change-legacy-k6")
			dataset := t.TempDir()
			writeWorkloadTestFile(t, filepath.Join(dataset, name, "pre."+cfg.FileType), "old-pre")
			// Legacy calls must not acquire a new filesystem dependency.
			writeWorkloadTestFile(t, filepath.Join(dataset, name, "workload.yaml"), "invalid: [")
			m := &ModuleInstance{}
			clients := m.newBackends(map[string]interface{}{
				"datasetPath": dataset,
				"backends":    []interface{}{name, map[string]interface{}{"type": name, "alias": "replica"}, map[string]interface{}{"type": name, "alias": "remote", "container": "", "connection": "explicit", "color": "blue"}},
			})
			for _, alias := range []string{name, "replica", "remote"} {
				if clients.Get(alias) == nil {
					t.Fatal("missing legacy client", alias)
				}
				got := metrics.GetBackendOptions(alias)
				wantContainer, wantConnection, wantColor := cfg.Container, cfg.DefaultConn, ""
				if alias == "replica" && cfg.Container != "" {
					wantContainer = "replica"
				}
				if alias == "remote" {
					wantContainer, wantConnection, wantColor = "", "explicit", "blue"
				}
				if got.Container != wantContainer || got.Color != wantColor || got.Alias != alias {
					t.Errorf("%s options: %+v", alias, got)
				}
				capture := metrics.GetBackendConfig(alias)
				if capture["connection_used"] != wantConnection || capture["pre_script"] != "old-pre" {
					t.Errorf("%s capture: %+v", alias, capture)
				}
			}
		})
	}
}

func TestMultipleWorkloadsShareEngineButKeepIndependentIdentity(t *testing.T) {
	fakeBackend(t, "postgres")
	dir, dataset := t.TempDir(), t.TempDir()
	for _, name := range []string{"fts", "bm25"} {
		writeWorkloadTestFile(t, filepath.Join(dir, name+".yaml"), "backend: postgres\nconnection: "+name+"-default\ncontainer: shared-postgres\ncolor: purple\n")
		writeWorkloadTestFile(t, filepath.Join(dataset, name, "pre.sql"), name+"-pre")
	}
	writeWorkloadTestFile(t, filepath.Join(dataset, "fts", "workload.yaml"), "connection: dataset-default\n")
	t.Setenv("FTS_URL", "fts-env")
	m := &ModuleInstance{}
	clients := m.newBackends(map[string]interface{}{
		"datasetPath": dataset, "workloadsDir": dir,
		"backends": []interface{}{
			map[string]interface{}{"type": "postgres", "workload": "fts"},
			map[string]interface{}{"type": "postgres", "workload": "bm25", "alias": "custom", "container": "", "connection": "explicit", "color": "red"},
		},
	})
	if clients.Get("fts") == nil || clients.Get("custom") == nil || clients.Get("postgres") != nil {
		t.Fatal("incorrect client identities")
	}
	if len(clients.telemetryProviders) != 2 {
		t.Fatal("telemetry identities collapsed")
	}
	if got := metrics.GetBackendOptions("fts"); !reflect.DeepEqual(got, &metrics.BackendOptions{Alias: "fts", Container: "shared-postgres", Color: "purple"}) {
		t.Fatalf("fts options: %+v", got)
	}
	if got := metrics.GetBackendOptions("custom"); !reflect.DeepEqual(got, &metrics.BackendOptions{Alias: "custom", Container: "", Color: "red"}) {
		t.Fatalf("custom options: %+v", got)
	}
	if got := metrics.GetBackendConfig("fts"); got["connection_used"] != "fts-env" || got["pre_script"] != "fts-pre" {
		t.Fatal(got)
	}
	if got := metrics.GetBackendConfig("custom"); got["connection_used"] != "explicit" || got["pre_script"] != "bm25-pre" {
		t.Fatal(got)
	}
}

func TestWorkloadConfigurationErrors(t *testing.T) {
	fakeBackend(t, "postgres")
	for _, tc := range []struct {
		blocks []interface{}
		want   string
	}{
		{[]interface{}{map[string]interface{}{"type": "postgres", "workload": 42}}, "non-empty string"},
		{[]interface{}{map[string]interface{}{"type": "mongodb", "workload": "paradedb"}}, "uses backend"},
		{[]interface{}{map[string]interface{}{"type": "postgres", "workload": "missing-workload"}}, "unknown workload"},
		{[]interface{}{map[string]interface{}{"type": "postgres", "workload": "postgres"}, map[string]interface{}{"type": "postgres", "workload": "postgres"}}, "duplicate alias"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			defer func() {
				got := recover()
				if got == nil || !strings.Contains(got.(string), tc.want) {
					t.Fatalf("panic = %v, want %s", got, tc.want)
				}
			}()
			(&ModuleInstance{}).newBackends(map[string]interface{}{"datasetPath": "", "backends": tc.blocks})
		})
	}
}
