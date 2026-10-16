// Package paradedb registers the ParadeDB backend.
package paradedb

import (
	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/backends/shared/postgres"
	"github.com/paradedb/benchmarker/workloads"
)

func init() {
	backends.Register("paradedb", backends.BackendConfig{
		Factory:     New,
		FileType:    "sql",
		EnvVar:      "PARADEDB_URL",
		DefaultConn: "postgres://postgres:postgres@localhost:5432/benchmark",
		Container:   "paradedb",
	})
}

// New retains the legacy Go API while constructing the embedded ParadeDB
// workload on the PostgreSQL driver. Filesystem manifests are opt-in.
func New(connString string) (backends.Driver, error) {
	workload, err := workloads.Builtin("paradedb")
	if err != nil {
		return nil, err
	}
	driver, err := postgres.New(connString)
	if err != nil {
		return nil, err
	}
	workload.ConfigureDriver(driver)
	return driver, nil
}
