// Package vectorchord registers the VectorChord backend.
package vectorchord

import (
	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/backends/shared/postgres"
	"github.com/paradedb/benchmarker/workloads"
)

func init() {
	backends.Register("vectorchord", backends.BackendConfig{
		Factory:     New,
		FileType:    "sql",
		EnvVar:      "VECTORCHORD_URL",
		DefaultConn: "postgres://postgres:postgres@localhost:5435/benchmark",
		Container:   "vectorchord",
	})
}

// New preserves the legacy constructor while taking extension-specific capture
// and telemetry configuration from the embedded vectorchord workload.
func New(connString string) (backends.Driver, error) {
	workload, err := workloads.Builtin("vectorchord")
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
