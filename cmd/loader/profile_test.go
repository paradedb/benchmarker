package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/profiles"
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
	want := []string{"clickhouse", "elasticsearch", "mongodb", "opensearch", "paradedb", "postgres"}
	loaders, err := selectLoaders("", "", profileOptions{})
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
			profile, _ := profiles.Builtin(name)
			cfg, _ := backends.GetConfig(profile.Backend)
			original := cfg
			var driver *selectionDriver
			cfg.Factory = func(conn string) (backends.Driver, error) {
				driver = &selectionDriver{connection: conn}
				return driver, nil
			}
			backends.Register(profile.Backend, cfg)
			t.Cleanup(func() { backends.Register(profile.Backend, original) })
			t.Setenv(profile.EnvironmentVariable(), "legacy-environment")
			selected, err := selectLoaders("", name, profileOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(selected) != 1 || selected[0].Name() != name {
				t.Fatal("selection changed")
			}
			if err := selected[0].Drop(context.Background(), &backends.Schema{Table: "documents"}); err != nil {
				t.Fatal(err)
			}
			if driver.connection != "legacy-environment" {
				t.Fatal("environment precedence changed")
			}
		})
	}
}

func TestProfileLoaderUsesEngineForSchemaAndDrop(t *testing.T) {
	for _, name := range []string{"postgres", "elasticsearch", "mongodb"} {
		t.Run(name, func(t *testing.T) {
			cfg, _ := backends.GetConfig(name)
			original := cfg
			var driver *selectionDriver
			cfg.Factory = func(conn string) (backends.Driver, error) {
				driver = &selectionDriver{connection: conn}
				return driver, nil
			}
			backends.Register(name, cfg)
			t.Cleanup(func() { backends.Register(name, original) })
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "custom.yaml"), []byte("backend: "+name+"\nconnection: manifest\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CUSTOM_URL", "environment")
			selected, err := selectLoaders("", "custom", profileOptions{connection: "explicit", dir: dir})
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
			if err := l.ValidateSchema(&backends.Schema{Table: "documents", Columns: map[string]string{"value": columnType}}); err != nil {
				t.Fatal(err)
			}
			if err := l.Drop(context.Background(), &backends.Schema{Table: "documents"}); err != nil {
				t.Fatal(err)
			}
			if driver.connection != "explicit" || len(driver.commands) != 1 {
				t.Fatalf("driver: %+v", driver)
			}
			want := map[string]string{
				"postgres":      `DROP TABLE IF EXISTS documents CASCADE`,
				"elasticsearch": `[{"index":"documents","method":"DELETE"}]`,
				"mongodb":       `{"collection":"documents","database":"benchmark","drop":true}`,
			}[name]
			if driver.commands[0] != want {
				t.Fatalf("drop=%s, want %s", driver.commands[0], want)
			}
		})
	}
}

func TestInvalidLoaderSelections(t *testing.T) {
	for _, tc := range []struct {
		backend string
		opts    profileOptions
	}{
		{"missing", profileOptions{}},
		{"", profileOptions{connection: "ambiguous"}},
		{"../paradedb", profileOptions{}},
		{"", profileOptions{dir: t.TempDir()}},
	} {
		if _, err := selectLoaders("", tc.backend, tc.opts); err == nil {
			t.Fatalf("invalid selection accepted: %+v", tc)
		}
	}
}

func TestProfileCLIFlagsAndSelection(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	opts := profileFlags(fs)
	if err := fs.Parse([]string{"--profiles-dir", "custom", "--connection", "explicit"}); err != nil || opts.dir != "custom" || opts.connection != "explicit" {
		t.Fatalf("profile flags: %+v, %v", opts, err)
	}
	for _, name := range []string{"paradedb", "postgres", "vectorsearch", "vectorchord"} {
		loaders, err := selectLoaders("", name, profileOptions{})
		if err != nil || len(loaders) != 1 || loaders[0].Name() != name {
			t.Fatalf("profile %s: %+v, %v", name, loaders, err)
		}
		if err := loaders[0].ValidateSchema(&backends.Schema{Table: "docs", Columns: map[string]string{"emb": "vector(3)"}}); err != nil {
			t.Fatal(err)
		}
	}
}
