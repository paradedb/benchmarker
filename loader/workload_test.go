package loader

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/paradedb/benchmarker/backends"
)

type workloadLoaderDriver struct {
	commands []string
	rows     int
	firstID  any
}

func (*workloadLoaderDriver) Close() error { return nil }
func (d *workloadLoaderDriver) Exec(_ context.Context, sql string) error {
	d.commands = append(d.commands, sql)
	return nil
}
func (*workloadLoaderDriver) Query(context.Context, string, ...any) (int, error) { return 0, nil }
func (d *workloadLoaderDriver) Insert(_ context.Context, _ string, cols []string, rows [][]any) (int, error) {
	d.rows += len(rows)
	for i, col := range cols {
		if col == "id" && len(rows) > 0 {
			d.firstID = rows[0][i]
		}
	}
	return len(rows), nil
}
func (*workloadLoaderDriver) Update(context.Context, string, []string, []string, [][]any) (int, error) {
	return 0, nil
}
func (*workloadLoaderDriver) CaptureConfig(context.Context, string) {}

func TestLoadWorkloadAndLegacyDatasetPaths(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "custom.yaml"), []byte("backend: loader-test-engine\nconnection: manifest\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var gotConn string
	var driver *workloadLoaderDriver
	backends.Register("loader-test-engine", backends.BackendConfig{FileType: "sql", Factory: func(conn string) (backends.Driver, error) {
		gotConn = conn
		driver = &workloadLoaderDriver{}
		return driver, nil
	}})
	schema := filepath.Join(dir, "schema.yaml")
	if err := os.WriteFile(schema, []byte("table: documents\ncolumns:\n  id: bigint\n  title: text\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, typed := range []bool{false, true} {
		for _, explicitWorkload := range []bool{false, true} {
			cfg := map[string]interface{}{"file": data, "dataset": dir}
			if typed {
				cfg["schema"] = schema
			}
			name := "loader-test-engine"
			if explicitWorkload {
				cfg["workload"] = "custom"
				cfg["workloadsDir"] = dir
				name = "custom"
			}
			result := (&Loader{}).Load("loader-test-engine", "explicit", cfg)
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
