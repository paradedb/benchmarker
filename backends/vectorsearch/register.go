// Package vectorsearch registers Timescale's pg_vectorsearch backend.
package vectorsearch

import (
	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/backends/shared/postgres"
	"github.com/paradedb/benchmarker/workloads"
)

func init() {
	backends.Register("vectorsearch", backends.BackendConfig{
		Factory:     New,
		FileType:    "sql",
		EnvVar:      "VECTORSEARCH_URL",
		DefaultConn: "postgres://postgres:postgres@localhost:5434/benchmark",
		Container:   "vectorsearch",
	})
}

// New preserves the legacy constructor while taking extension-specific capture
// and telemetry configuration from the embedded vectorsearch workload.
func New(connString string) (backends.Driver, error) {
	workload, err := workloads.Builtin("vectorsearch")
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
