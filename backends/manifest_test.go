package backends_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/drivers"
	_ "github.com/paradedb/benchmarker/drivers/clickhouse"
	_ "github.com/paradedb/benchmarker/drivers/elasticsearch"
	_ "github.com/paradedb/benchmarker/drivers/mongodb"
	_ "github.com/paradedb/benchmarker/drivers/opensearch"
	_ "github.com/paradedb/benchmarker/drivers/postgres"
	_ "github.com/paradedb/benchmarker/drivers/turbopuffer"
)

func write(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltinDefaultsOutsideCheckout(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct{ name, connection, container, env string }{
		{"clickhouse", "clickhouse://default:clickhouse@localhost:9000/default", "clickhouse", "CLICKHOUSE_URL"},
		{"elasticsearch", "http://localhost:9200", "elasticsearch", "ELASTICSEARCH_URL"},
		{"mongodb", "mongodb://localhost:27017", "mongodb", "MONGODB_URL"},
		{"opensearch", "http://localhost:9201", "opensearch", "OPENSEARCH_URL"},
		{"postgres", "postgres://postgres:postgres@localhost:5433/benchmark", "postgres", "POSTGRES_URL"},
		{"turbopuffer", "", "", "TURBOPUFFER_URL"},
	} {
		w, err := backends.Resolve(tc.name, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if w.DriverName != tc.name || w.Connection != tc.connection || w.ContainerName() != tc.container || w.EnvironmentVariable() != tc.env {
			t.Errorf("%s defaults changed: %+v", tc.name, w)
		}
		if _, err := w.DriverConfig(); err != nil {
			t.Fatal(err)
		}
	}
	w, err := backends.Builtin("paradedb")
	if err != nil {
		t.Fatal(err)
	}
	if _, registered := drivers.GetConfig("paradedb"); registered {
		t.Fatal("ParadeDB must be a backend, not a registered driver")
	}
	if w.Connection != "postgres://postgres:postgres@localhost:5432/benchmark" || w.ContainerName() != "paradedb" || w.EnvironmentVariable() != "PARADEDB_URL" {
		t.Fatalf("ParadeDB defaults changed: %+v", w)
	}
	if w.DriverName != "postgres" || !reflect.DeepEqual(w.Extras.GUCPrefixes, []string{"paradedb"}) ||
		!reflect.DeepEqual(w.Extras.IndexAccessMethods, []string{"paradedb", "bm25"}) || len(w.Extras.Queries) != 2 || w.Extras.Queries[0].Key != "paradedb.version" {
		t.Fatalf("lost ParadeDB capture/telemetry: %+v", w)
	}
	fts, err := backends.Builtin("postgresfts")
	if err != nil || fts.EnvironmentVariable() != "POSTGRES_URL" {
		t.Fatalf("FTS env compatibility: %+v, %v", fts, err)
	}
}

func TestOverrideIsShallowAndPreservesExplicitEmptyValues(t *testing.T) {
	dir, dataset := t.TempDir(), t.TempDir()
	write(t, filepath.Join(dir, "custom.yaml"), `driver: postgres
connection: global
container: global-container
color: red
extras:
  gucs: [work_mem]
  queries: ["SELECT 'global' AS version"]
`)
	write(t, filepath.Join(dataset, "custom", "backend.yaml"), `connection: dataset
container: ""
color: ""
extras:
  queries:
    - SELECT 'dataset' AS custom_version
    - key: stable.key
      query: SELECT 'legacy'
`)
	w, err := backends.Resolve("custom", dataset, dir)
	if err != nil {
		t.Fatal(err)
	}
	if w.DriverName != "postgres" || w.Connection != "dataset" || w.ContainerName() != "" || w.Color != "" || len(w.Extras.GUCs) != 0 || len(w.Extras.Queries) != 2 {
		t.Fatalf("bad shallow merge: %+v", w)
	}
	if w.Extras.Queries[0].Key != "" || w.Extras.Queries[1].Key != "stable.key" {
		t.Fatalf("query keys: %+v", w.Extras.Queries)
	}
	write(t, filepath.Join(dataset, "custom", "backend.yaml"), "extras: {}\n")
	w, err = backends.Resolve("custom", dataset, dir)
	if err != nil || len(w.Extras.Queries) != 0 || w.ContainerName() != "global-container" {
		t.Fatalf("clear extras: %+v, %v", w, err)
	}
}

func TestConnectionPrecedenceAndCustomDefaults(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "custom-search.yaml"), "driver: postgres\nconnection: manifest\n")
	w, err := backends.Resolve("custom-search", "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if w.ContainerName() != "" || w.EnvironmentVariable() != "CUSTOM_SEARCH_URL" {
		t.Fatalf("defaults: %+v", w)
	}
	t.Setenv("CUSTOM_SEARCH_URL", "")
	if got, err := w.ResolveConnection(""); err != nil || got != "manifest" {
		t.Fatalf("manifest = %q, %v", got, err)
	}
	t.Setenv("CUSTOM_SEARCH_URL", "environment")
	if got, _ := w.ResolveConnection(""); got != "environment" {
		t.Fatal(got)
	}
	if got, _ := w.ResolveConnection("explicit"); got != "explicit" {
		t.Fatal(got)
	}
	t.Setenv("CUSTOM_SEARCH_URL", "")
	w.Connection = ""
	if _, err := w.ResolveConnection(""); err == nil {
		t.Fatal("missing connection accepted")
	}
}

func TestTINRequiresRemoteConnection(t *testing.T) {
	t.Chdir(t.TempDir())
	w, err := backends.Resolve("tin", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if w.DriverName != "postgres" || w.ContainerName() != "" || w.Connection != "" || w.EnvironmentVariable() != "TIN_URL" {
		t.Fatalf("unexpected TIN defaults: %+v", w)
	}
	t.Setenv("TIN_URL", "")
	if _, err := w.ResolveConnection(""); err == nil || !strings.Contains(err.Error(), "set connection or TIN_URL") {
		t.Fatalf("expected actionable missing connection error, got %v", err)
	}
	t.Setenv("TIN_URL", "postgres://remote/benchmark")
	if got, err := w.ResolveConnection(""); err != nil || got != "postgres://remote/benchmark" {
		t.Fatalf("environment connection = %q, %v", got, err)
	}
	if got, err := w.ResolveConnection("postgres://explicit/benchmark"); err != nil || got != "postgres://explicit/benchmark" {
		t.Fatalf("explicit connection = %q, %v", got, err)
	}
}

func TestMalformedManifestsFail(t *testing.T) {
	for _, data := range []string{
		"null\n", "{}\n", "driver: postgres\ndriver: mongodb\n",
		"driver: postgres\nconection: typo\n", "driver: postgres\n---\ndriver: mongodb\n",
		"driver: postgres\nextras:\n  unknown: true\n",
		"driver: postgres\nextras:\n  queries: [42]\n",
		"driver: postgres\nextras:\n  queries: ['']\n",
		"driver: postgres\nextras:\n  queries: [{query: SELECT 1, typo: bad}]\n",
	} {
		t.Run(data, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "bad.yaml"), data)
			if _, err := backends.Resolve("bad", t.TempDir(), dir); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	for _, name := range []string{"", "../paradedb", "/paradedb", "a/b", "a\\b"} {
		if _, err := backends.Resolve(name, "", ""); err == nil {
			t.Fatalf("invalid name accepted: %q", name)
		}
	}
	if _, err := backends.Resolve("unknown", "", t.TempDir()); err == nil {
		t.Fatal("missing manifest accepted")
	}
	dir := t.TempDir()
	if _, err := backends.Resolve("postgres", "", filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing explicit directory accepted")
	}
}

func TestUnsupportedDriversAndExtras(t *testing.T) {
	w, _ := backends.Builtin("paradedb")
	w.DriverName = "mongodb"
	if _, err := w.DriverConfig(); err == nil {
		t.Fatal("postgres extras accepted for MongoDB")
	}
	w.DriverName = "unregistered"
	if _, err := w.DriverConfig(); err == nil {
		t.Fatal("unknown backend accepted")
	}
}

func TestRegisteredDriverRequiresBackendManifest(t *testing.T) {
	drivers.Register("custom-http-driver", drivers.DriverConfig{FileType: "json"})
	dir, dataset := t.TempDir(), t.TempDir()
	// Dataset overrides cannot substitute for a global or embedded manifest.
	write(t, filepath.Join(dataset, "custom-http-driver", "backend.yaml"), "driver: custom-http-driver\nconnection: http://custom\n")
	if _, err := backends.Resolve("custom-http-driver", dataset, dir); err == nil {
		t.Fatal("registered driver accepted without a backend manifest")
	}
	for _, name := range backends.DefaultNames() {
		if name == "custom-http-driver" {
			t.Fatal("driver registration added a default backend")
		}
	}
	write(t, filepath.Join(dir, "custom.yaml"), "driver: custom-http-driver\nconnection: http://custom\ncontainer: \"\"\n")
	w, err := backends.Resolve("custom", dataset, dir)
	if err != nil {
		t.Fatal(err)
	}
	if w.DriverName != "custom-http-driver" || w.Connection != "http://custom" || w.ContainerName() != "" {
		t.Fatalf("backend did not select the driver: %+v", w)
	}
	if _, err := w.DriverConfig(); err != nil {
		t.Fatal(err)
	}
}
