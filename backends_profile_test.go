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
	"github.com/paradedb/benchmarker/profiles"
)

type profileTestDriver struct{ connection string }

func (*profileTestDriver) Close() error                                       { return nil }
func (*profileTestDriver) Exec(context.Context, string) error                 { return nil }
func (*profileTestDriver) Query(context.Context, string, ...any) (int, error) { return 0, nil }
func (*profileTestDriver) Insert(context.Context, string, []string, [][]any) (int, error) {
	return 0, nil
}
func (*profileTestDriver) Update(context.Context, string, []string, []string, [][]any) (int, error) {
	return 0, nil
}
func (d *profileTestDriver) CaptureConfig(_ context.Context, alias string) {
	metrics.RegisterBackendConfig(alias, map[string]interface{}{"connection_used": d.connection})
}
func (*profileTestDriver) TelemetryEnabled() bool { return true }
func (*profileTestDriver) ReadTelemetry(context.Context) ([]metrics.TelemetryPoint, error) {
	return nil, nil
}

func fakeBackend(t *testing.T, name string) {
	t.Helper()
	original, ok := backends.GetConfig(name)
	if !ok {
		t.Fatal(name)
	}
	cfg := original
	cfg.Factory = func(conn string) (backends.Driver, error) { return &profileTestDriver{connection: conn}, nil }
	backends.Register(name, cfg)
	t.Cleanup(func() { backends.Register(name, original) })
}

func writeProfileTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyBackendConfigurationsRetainDefaults(t *testing.T) {
	for _, name := range profiles.DefaultNames() {
		t.Run(name, func(t *testing.T) {
			p, err := profiles.Builtin(name)
			if err != nil {
				t.Fatal(err)
			}
			fakeBackend(t, p.Backend)
			cfg, _ := backends.GetConfig(p.Backend)
			t.Setenv(p.EnvironmentVariable(), "must-not-change-legacy-k6")
			dataset := t.TempDir()
			writeProfileTestFile(t, filepath.Join(dataset, name, "pre."+cfg.FileType), "old-pre")
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
				w, _ := profiles.Builtin(name)
				wantContainer, wantConnection, wantColor := w.ContainerName(), w.Connection, w.Color
				if alias == "replica" {
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

func TestMultipleProfilesShareEngineButKeepIndependentIdentity(t *testing.T) {
	fakeBackend(t, "postgres")
	dir, dataset := t.TempDir(), t.TempDir()
	for _, name := range []string{"fts", "bm25"} {
		writeProfileTestFile(t, filepath.Join(dir, name+".yaml"), "backend: postgres\nconnection: "+name+"-default\ncontainer: shared-postgres\ncolor: purple\n")
		writeProfileTestFile(t, filepath.Join(dataset, name, "pre.sql"), name+"-pre")
	}
	writeProfileTestFile(t, filepath.Join(dataset, "fts", "profile.yaml"), "connection: dataset-default\n")
	t.Setenv("FTS_URL", "fts-env")
	m := &ModuleInstance{}
	clients := m.newBackends(map[string]interface{}{
		"datasetPath": dataset, "profilesDir": dir,
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

func TestProfileConfigurationErrors(t *testing.T) {
	fakeBackend(t, "postgres")
	for _, tc := range []struct {
		blocks []interface{}
		want   string
	}{
		{[]interface{}{map[string]interface{}{"type": 42}}, "type"},
		{[]interface{}{map[string]interface{}{"type": "missing-profile"}}, "unknown profile"},
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
	for _, name := range []string{"paradedb", "vectorchord", "vectorsearch"} {
		t.Run(name, func(t *testing.T) {
			if _, registered := backends.GetConfig(name); registered {
				t.Fatal("extension registered as a driver", name)
			}
			p, _ := profiles.Builtin(name)
			t.Setenv(p.EnvironmentVariable(), "from-environment")
			clients := (&ModuleInstance{}).newBackends(map[string]interface{}{
				"backends": []interface{}{name},
			})
			if clients.Get(name) == nil {
				t.Fatal("profile identity lost")
			}
			want := "from-environment"
			if name == "paradedb" {
				want = p.Connection // retain the original k6 connection semantics
			}
			if got := metrics.GetBackendConfig(name)["connection_used"]; got != want {
				t.Fatalf("connection = %v, want %s", got, want)
			}

		})
	}
}
