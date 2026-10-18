package search

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/drivers"
	"github.com/paradedb/benchmarker/metrics"
)

type backendTestDriver struct{ connection string }

func (*backendTestDriver) Close() error                                       { return nil }
func (*backendTestDriver) Exec(context.Context, string) error                 { return nil }
func (*backendTestDriver) Query(context.Context, string, ...any) (int, error) { return 0, nil }
func (*backendTestDriver) Insert(context.Context, string, []string, [][]any) (int, error) {
	return 0, nil
}
func (*backendTestDriver) Update(context.Context, string, []string, []string, [][]any) (int, error) {
	return 0, nil
}
func (d *backendTestDriver) CaptureConfig(_ context.Context, alias string) {
	metrics.RegisterBackendConfig(alias, map[string]interface{}{"connection_used": d.connection})
}
func (*backendTestDriver) TelemetryEnabled() bool { return true }
func (*backendTestDriver) ReadTelemetry(context.Context) ([]metrics.TelemetryPoint, error) {
	return nil, nil
}

func fakeBackend(t *testing.T, name string) {
	t.Helper()
	original, ok := drivers.GetConfig(name)
	if !ok {
		t.Fatal(name)
	}
	cfg := original
	cfg.Factory = func(conn string) (drivers.Driver, error) { return &backendTestDriver{connection: conn}, nil }
	drivers.Register(name, cfg)
	t.Cleanup(func() { drivers.Register(name, original) })
}

func writeBackendTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestBackendDefaultsAndConnectionOverrides(t *testing.T) {
	for _, name := range backends.DefaultNames() {
		t.Run(name, func(t *testing.T) {
			p, err := backends.Resolve(name, "", "")
			if err != nil {
				t.Fatal(err)
			}
			fakeBackend(t, p.DriverName)
			cfg, _ := drivers.GetConfig(p.DriverName)
			t.Setenv(p.EnvironmentVariable(), "from-environment")
			dataset := t.TempDir()
			writeBackendTestFile(t, filepath.Join(dataset, name, "pre."+cfg.FileType), "old-pre")
			m := &ModuleInstance{}
			clients := m.newBackends(map[string]interface{}{
				"datasetPath": dataset,
				"backends":    []interface{}{name, map[string]interface{}{"type": name, "alias": "replica"}, map[string]interface{}{"type": name, "alias": "remote", "container": "", "connection": "explicit", "color": "blue"}},
			})
			for _, alias := range []string{name, "replica", "remote"} {
				if clients.Get(alias) == nil {
					t.Fatal("missing backend client", alias)
				}
				got := metrics.GetBackendOptions(alias)
				wantContainer, wantConnection, wantColor := p.ContainerName(), "from-environment", p.Color
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

func TestMultipleBackendsShareEngineButKeepIndependentIdentity(t *testing.T) {
	fakeBackend(t, "postgres")
	dir, dataset := t.TempDir(), t.TempDir()
	for _, name := range []string{"fts", "bm25"} {
		writeBackendTestFile(t, filepath.Join(dir, name+".yaml"), "driver: postgres\nconnection: "+name+"-default\ncontainer: shared-postgres\ncolor: purple\n")
		writeBackendTestFile(t, filepath.Join(dataset, name, "pre.sql"), name+"-pre")
	}
	writeBackendTestFile(t, filepath.Join(dataset, "fts", "backend.yaml"), "connection: dataset-default\n")
	t.Setenv("FTS_URL", "fts-env")
	m := &ModuleInstance{}
	clients := m.newBackends(map[string]interface{}{
		"datasetPath": dataset, "backendsDir": dir,
		"backends": []interface{}{
			map[string]interface{}{"type": "fts"},
			map[string]interface{}{"type": "bm25", "alias": "custom", "container": "", "connection": "explicit", "color": "red"},
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

func TestBackendConfigurationErrors(t *testing.T) {
	fakeBackend(t, "postgres")
	drivers.Register("driver-without-manifest", drivers.DriverConfig{Factory: func(string) (drivers.Driver, error) {
		t.Fatal("driver instantiated without a backend manifest")
		return nil, nil
	}})
	for _, tc := range []struct {
		blocks []interface{}
		want   string
	}{
		{[]interface{}{map[string]interface{}{"type": 42}}, "type"},
		{[]interface{}{map[string]interface{}{"type": "missing-backend"}}, "unknown backend"},
		{[]interface{}{map[string]interface{}{"type": "driver-without-manifest", "connection": "explicit"}}, "no manifest found"},
		{[]interface{}{map[string]interface{}{"type": "postgres"}, map[string]interface{}{"type": "postgres"}}, "duplicate alias"},
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

func TestPostgresExtensionTypesResolveWithoutDriverAliases(t *testing.T) {
	fakeBackend(t, "postgres")
	for _, name := range []string{"paradedb", "vectorchord", "pg_vectorsearch"} {
		t.Run(name, func(t *testing.T) {
			if _, registered := drivers.GetConfig(name); registered {
				t.Fatal("extension registered as a driver", name)
			}
			p, _ := backends.Builtin(name)
			t.Setenv(p.EnvironmentVariable(), "from-environment")
			clients := (&ModuleInstance{}).newBackends(map[string]interface{}{
				"backends": []interface{}{name},
			})
			if clients.Get(name) == nil {
				t.Fatal("backend identity lost")
			}
			want := "from-environment"
			if got := metrics.GetBackendConfig(name)["connection_used"]; got != want {
				t.Fatalf("connection = %v, want %s", got, want)
			}

		})
	}
}
