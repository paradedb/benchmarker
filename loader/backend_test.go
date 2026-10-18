package loader

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/paradedb/benchmarker/drivers"
)

type backendLoaderDriver struct {
	commands []string
	rows     int
	firstID  any
}

func (*backendLoaderDriver) Close() error { return nil }
func (d *backendLoaderDriver) Exec(_ context.Context, sql string) error {
	d.commands = append(d.commands, sql)
	return nil
}
func (*backendLoaderDriver) Query(context.Context, string, ...any) (int, error) { return 0, nil }
func (d *backendLoaderDriver) Insert(_ context.Context, _ string, cols []string, rows [][]any) (int, error) {
	d.rows += len(rows)
	for i, col := range cols {
		if col == "id" && len(rows) > 0 {
			d.firstID = rows[0][i]
		}
	}
	return len(rows), nil
}
func (*backendLoaderDriver) Update(context.Context, string, []string, []string, [][]any) (int, error) {
	return 0, nil
}
func (*backendLoaderDriver) CaptureConfig(context.Context, string) {}

func TestLoadRequiresBackendManifest(t *testing.T) {
	drivers.Register("driver-without-manifest", drivers.DriverConfig{Factory: func(string) (drivers.Driver, error) {
		t.Fatal("driver instantiated without a backend manifest")
		return nil, nil
	}})
	for _, typed := range []bool{false, true} {
		config := map[string]interface{}{"file": "unused.csv", "backendsDir": t.TempDir()}
		if typed {
			config["schema"] = "unused.yaml"
		}
		result := (&Loader{}).Load("driver-without-manifest", "explicit", config)
		if err, ok := result["error"].(string); !ok || !strings.Contains(err, "no manifest found") {
			t.Fatalf("missing manifest not rejected: %v", result)
		}
	}
}

func TestLoadBackendDatasetPaths(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(data, []byte("id,title\n1,hello\n2,world\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"loader-test-engine", "custom"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"pre", "post"} {
			if err := os.WriteFile(filepath.Join(dir, name, phase+".sql"), []byte(name+"-"+phase), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, name := range []string{"loader-test-engine", "custom"} {
		if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte("driver: loader-test-engine\nconnection: manifest\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var gotConn string
	var driver *backendLoaderDriver
	drivers.Register("loader-test-engine", drivers.DriverConfig{FileType: "sql", Factory: func(conn string) (drivers.Driver, error) {
		gotConn = conn
		driver = &backendLoaderDriver{}
		return driver, nil
	}})
	schema := filepath.Join(dir, "schema.yaml")
	if err := os.WriteFile(schema, []byte("table: documents\ncolumns:\n  id: bigint\n  title: text\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, typed := range []bool{false, true} {
		for _, explicitBackend := range []bool{false, true} {
			cfg := map[string]interface{}{"file": data, "dataset": dir, "backendsDir": dir}
			if typed {
				cfg["schema"] = schema
			}
			name := "loader-test-engine"
			if explicitBackend {
				name = "custom"
			}
			result := (&Loader{}).Load(name, "explicit", cfg)
			if result["error"] != nil {
				t.Fatal(result)
			}
			if gotConn != "explicit" || driver.rows != 2 || !reflect.DeepEqual(driver.commands, []string{name + "-pre", name + "-post"}) {
				t.Fatalf("connection=%s, driver=%+v", gotConn, driver)
			}
			if typed {
				if _, ok := driver.firstID.(int64); !ok {
					t.Fatalf("typed load lost ID type: %T", driver.firstID)
				}
			}
		}
	}
}
