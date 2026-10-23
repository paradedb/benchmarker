package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/drivers"
)

type selectionDriver struct {
	connection string
	commands   []string
}

func (*selectionDriver) Close() error { return nil }
func (d *selectionDriver) Exec(_ context.Context, sql string) error {
	d.commands = append(d.commands, sql)
	return nil
}
func (*selectionDriver) Query(context.Context, string, ...any) (int, error) { return 0, nil }
func (*selectionDriver) Insert(context.Context, string, []string, [][]any) (int, error) {
	return 0, nil
}
func (*selectionDriver) Update(context.Context, string, []string, []string, [][]any) (int, error) {
	return 0, nil
}
func (*selectionDriver) CaptureConfig(context.Context, string) {}

func TestLegacyLoaderSelectionAndEnvironment(t *testing.T) {
	want := []string{"clickhouse", "elasticsearch", "mongodb", "opensearch", "paradedb", "postgres", "turbopuffer"}
	loaders, err := selectLoaders("", "", backendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range loaders {
		got = append(got, l.Name())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy enumeration = %v, want %v", got, want)
	}
	for _, name := range want {
		t.Run(name, func(t *testing.T) {
			backend, _ := backends.Resolve(name, "", "")
			cfg, _ := drivers.GetConfig(backend.DriverName)
			original := cfg
			var driver *selectionDriver
			cfg.Factory = func(conn string) (drivers.Driver, error) {
				driver = &selectionDriver{connection: conn}
				return driver, nil
			}
			drivers.Register(backend.DriverName, cfg)
			t.Cleanup(func() { drivers.Register(backend.DriverName, original) })
			t.Setenv(backend.EnvironmentVariable(), "")
			defaults, err := selectLoaders("", name, backendOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if err := defaults[0].Drop(context.Background(), &drivers.Schema{Table: "documents"}); err != nil {
				t.Fatal(err)
			}
			if driver.connection != backend.Connection {
				t.Fatalf("default connection = %q, want %q", driver.connection, backend.Connection)
			}
			t.Setenv(backend.EnvironmentVariable(), "legacy-environment")
			selected, err := selectLoaders("", name, backendOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(selected) != 1 || selected[0].Name() != name {
				t.Fatal("selection changed")
			}
			if err := selected[0].Drop(context.Background(), &drivers.Schema{Table: "documents"}); err != nil {
				t.Fatal(err)
			}
			if driver.connection != "legacy-environment" {
				t.Fatal("environment precedence changed")
			}
		})
	}
}

func TestBackendLoaderUsesEngineForSchemaAndDrop(t *testing.T) {
	for _, name := range []string{"postgres", "elasticsearch", "mongodb", "turbopuffer"} {
		t.Run(name, func(t *testing.T) {
			cfg, _ := drivers.GetConfig(name)
			original := cfg
			var driver *selectionDriver
			cfg.Factory = func(conn string) (drivers.Driver, error) {
				driver = &selectionDriver{connection: conn}
				return driver, nil
			}
			drivers.Register(name, cfg)
			t.Cleanup(func() { drivers.Register(name, original) })
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "custom.yaml"), []byte("driver: "+name+"\nconnection: manifest\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CUSTOM_URL", "environment")
			selected, err := selectLoaders("", "custom", backendOptions{connection: "explicit", dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			l := selected[0]
			if l.Name() != "custom" {
				t.Fatal(l.Name())
			}
			columnType := "vector(3)"
			if name == "mongodb" {
				columnType = "text"
			}
			if err := l.ValidateSchema(&drivers.Schema{Table: "documents", Columns: map[string]string{"value": columnType}}); err != nil {
				t.Fatal(err)
			}
			if err := l.Drop(context.Background(), &drivers.Schema{Table: "documents"}); err != nil {
				t.Fatal(err)
			}
			if driver.connection != "explicit" || len(driver.commands) != 1 {
				t.Fatalf("driver: %+v", driver)
			}
			want := map[string]string{
				"postgres":      `DROP TABLE IF EXISTS documents CASCADE`,
				"elasticsearch": `[{"index":"documents","method":"DELETE"}]`,
				"mongodb":       `{"collection":"documents","database":"benchmark","drop":true}`,
				"turbopuffer":   `[{"method":"DELETE","namespace":"documents"}]`,
			}[name]
			if driver.commands[0] != want {
				t.Fatalf("drop=%s, want %s", driver.commands[0], want)
			}
		})
	}
}

func TestInvalidLoaderSelections(t *testing.T) {
	drivers.Register("driver-without-manifest", drivers.DriverConfig{FileType: "sql"})
	for _, tc := range []struct {
		backend string
		opts    backendOptions
	}{
		{"missing", backendOptions{}},
		{"driver-without-manifest", backendOptions{connection: "explicit"}},
		{"", backendOptions{connection: "ambiguous"}},
		{"../paradedb", backendOptions{}},
		{"", backendOptions{dir: t.TempDir()}},
	} {
		if _, err := selectLoaders("", tc.backend, tc.opts); err == nil {
			t.Fatalf("invalid selection accepted: %+v", tc)
		}
	}
}

func TestBackendCLIFlagsAndSelection(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	opts := backendFlags(fs)
	if err := fs.Parse([]string{"--backends-dir", "custom", "--connection", "explicit"}); err != nil || opts.dir != "custom" || opts.connection != "explicit" {
		t.Fatalf("backend flags: %+v, %v", opts, err)
	}
	for _, name := range []string{"paradedb", "postgres", "pg_vectorsearch", "vectorchord"} {
		loaders, err := selectLoaders("", name, backendOptions{})
		if err != nil || len(loaders) != 1 || loaders[0].Name() != name {
			t.Fatalf("backend %s: %+v, %v", name, loaders, err)
		}
		if err := loaders[0].ValidateSchema(&drivers.Schema{Table: "docs", Columns: map[string]string{"emb": "vector(3)"}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPostOnlyWithoutLocalCorpus(t *testing.T) {
	dir := t.TempDir()
	putTestFile(t, dir, "schema.yaml", []byte("table: documents\ncolumns: {id: bigint}\n"))
	putTestFile(t, dir, "postgres/pre.sql", []byte("SELECT 'pre';"))
	putTestFile(t, dir, "postgres/post.sql", []byte("SELECT 'post';"))
	cfg, _ := drivers.GetConfig("postgres")
	original := cfg
	var driver *selectionDriver
	cfg.Factory = func(string) (drivers.Driver, error) { driver = &selectionDriver{}; return driver, nil }
	drivers.Register("postgres", cfg)
	t.Cleanup(func() { drivers.Register("postgres", original) })
	runLoad(dir, "postgres", 100, 1, loadPostOnly, backendOptions{})
	if driver == nil || len(driver.commands) != 1 || driver.commands[0] != "SELECT 'post';" {
		t.Fatalf("post-only commands: %+v", driver)
	}
}
