package workloads_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/paradedb/benchmarker/backends"
	_ "github.com/paradedb/benchmarker/backends/clickhouse"
	_ "github.com/paradedb/benchmarker/backends/elasticsearch"
	_ "github.com/paradedb/benchmarker/backends/mongodb"
	_ "github.com/paradedb/benchmarker/backends/opensearch"
	_ "github.com/paradedb/benchmarker/backends/paradedb"
	_ "github.com/paradedb/benchmarker/backends/postgres"
	_ "github.com/paradedb/benchmarker/backends/vectorchord"
	_ "github.com/paradedb/benchmarker/backends/vectorsearch"
	"github.com/paradedb/benchmarker/workloads"
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

func TestBuiltinsPreserveRegisteredDefaultsOutsideCheckout(t *testing.T) {
	t.Chdir(t.TempDir())
	wantNames := []string{"clickhouse", "elasticsearch", "mongodb", "opensearch", "paradedb", "postgres", "vectorchord", "vectorsearch"}
	for _, name := range wantNames {
		if _, ok := backends.GetConfig(name); !ok {
			t.Fatalf("legacy registry entry missing: %s", name)
		}
	}
	for _, name := range wantNames {
		w, err := workloads.Resolve(name, "", "")
		if err != nil {
			t.Fatal(err)
		}
		cfg, _ := backends.GetConfig(name)
		if w.Connection != cfg.DefaultConn || w.ContainerName() != cfg.Container || w.EnvironmentVariable() != cfg.EnvVar {
			t.Errorf("%s defaults differ: %+v versus %+v", name, w, cfg)
		}
		if _, err := w.BackendConfig(); err != nil {
			t.Fatal(err)
		}
	}
	w, err := workloads.Builtin("paradedb")
	if err != nil {
		t.Fatal(err)
	}
	if w.Backend != "postgres" || !reflect.DeepEqual(w.Extras.GUCPrefixes, []string{"paradedb"}) ||
		!reflect.DeepEqual(w.Extras.IndexAccessMethods, []string{"paradedb", "bm25"}) || len(w.Extras.Queries) != 2 || w.Extras.Queries[0].Key != "paradedb.version" {
		t.Fatalf("lost ParadeDB capture/telemetry: %+v", w)
	}
	fts, err := workloads.Builtin("postgresfts")
	if err != nil || fts.EnvironmentVariable() != "POSTGRES_URL" {
		t.Fatalf("FTS env compatibility: %+v, %v", fts, err)
	}
}

func TestOverrideIsShallowAndPreservesExplicitEmptyValues(t *testing.T) {
	dir, dataset := t.TempDir(), t.TempDir()
	write(t, filepath.Join(dir, "custom.yaml"), `backend: postgres
connection: global
container: global-container
color: red
extras:
  gucs: [work_mem]
  queries: ["SELECT 'global' AS version"]
`)
	write(t, filepath.Join(dataset, "custom", "workload.yaml"), `connection: dataset
container: ""
color: ""
extras:
  queries:
    - SELECT 'dataset' AS custom_version
    - key: stable.key
      query: SELECT 'legacy'
`)
	w, err := workloads.Resolve("custom", dataset, dir)
	if err != nil {
		t.Fatal(err)
	}
	if w.Backend != "postgres" || w.Connection != "dataset" || w.ContainerName() != "" || w.Color != "" || len(w.Extras.GUCs) != 0 || len(w.Extras.Queries) != 2 {
		t.Fatalf("bad shallow merge: %+v", w)
	}
	if w.Extras.Queries[0].Key != "" || w.Extras.Queries[1].Key != "stable.key" {
		t.Fatalf("query keys: %+v", w.Extras.Queries)
	}
	write(t, filepath.Join(dataset, "custom", "workload.yaml"), "extras: {}\n")
	w, err = workloads.Resolve("custom", dataset, dir)
	if err != nil || len(w.Extras.Queries) != 0 || w.ContainerName() != "global-container" {
		t.Fatalf("clear extras: %+v, %v", w, err)
	}
}

func TestConnectionPrecedenceAndCustomDefaults(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "custom-search.yaml"), "backend: postgres\nconnection: manifest\n")
	w, err := workloads.Resolve("custom-search", "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if w.ContainerName() != "custom-search" || w.EnvironmentVariable() != "CUSTOM_SEARCH_URL" {
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

func TestMalformedManifestsFail(t *testing.T) {
	for _, data := range []string{
		"null\n", "{}\n", "backend: postgres\nbackend: mongodb\n",
		"backend: postgres\nconection: typo\n", "backend: postgres\n---\nbackend: mongodb\n",
		"backend: postgres\nextras:\n  unknown: true\n",
		"backend: postgres\nextras:\n  queries: [42]\n",
		"backend: postgres\nextras:\n  queries: ['']\n",
		"backend: postgres\nextras:\n  queries: [{query: SELECT 1, typo: bad}]\n",
	} {
		t.Run(data, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "bad.yaml"), data)
			if _, err := workloads.Resolve("bad", t.TempDir(), dir); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	for _, name := range []string{"", "../paradedb", "/paradedb", "a/b", "a\\b"} {
		if _, err := workloads.Resolve(name, "", ""); err == nil {
			t.Fatalf("invalid name accepted: %q", name)
		}
	}
	if _, err := workloads.Resolve("unknown", "", t.TempDir()); err == nil {
		t.Fatal("missing manifest accepted")
	}
	dir := t.TempDir()
	if _, err := workloads.Resolve("postgres", "", filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing explicit directory accepted")
	}
}

func TestBackendMismatchAndUnsupportedExtras(t *testing.T) {
	w, _ := workloads.Builtin("paradedb")
	for _, name := range []string{"", "postgres", "paradedb"} {
		if err := w.CheckBackend(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.CheckBackend("mongodb"); err == nil {
		t.Fatal("mismatch accepted")
	}
	w.Backend = "mongodb"
	if _, err := w.BackendConfig(); err == nil {
		t.Fatal("postgres extras accepted for MongoDB")
	}
	w.Backend = "unregistered"
	if _, err := w.BackendConfig(); err == nil {
		t.Fatal("unknown backend accepted")
	}
}

func TestRegisteredBackendWithoutManifest(t *testing.T) {
	backends.Register("custom-http-driver", backends.BackendConfig{FileType: "json", DefaultConn: "http://custom", EnvVar: "CUSTOM_HTTP_URL", Container: ""})
	w, err := workloads.Resolve("custom-http-driver", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if w.Backend != "custom-http-driver" || w.Connection != "http://custom" || w.ContainerName() != "" || w.EnvironmentVariable() != "CUSTOM_HTTP_URL" {
		t.Fatalf("fallback lost registry defaults: %+v", w)
	}
	if _, err := w.BackendConfig(); err != nil {
		t.Fatal(err)
	}
}
