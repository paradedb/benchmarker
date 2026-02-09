// Package paradedb registers the ParadeDB backend.
package paradedb

import (
	"github.com/paradedb/benchmarks/backends"
	"github.com/paradedb/benchmarks/backends/shared/postgres"
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

// New creates a new ParadeDB driver with ParadeDB-specific GUC capture.
func New(connString string) (backends.Driver, error) {
	driver, err := postgres.New(connString)
	if err != nil {
		return nil, err
	}

	// Add ParadeDB-specific GUCs to capture
	driver.(*postgres.Driver).SetExtraGUCs([]string{
		"paradedb.global_mutable_segment_rows",
		"paradedb.global_target_segment_size",
	})

	return driver, nil
}
