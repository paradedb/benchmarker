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

	pgDriver := driver.(*postgres.Driver)

	// Add ParadeDB-specific GUCs to capture
	pgDriver.SetExtraGUCs([]string{
		"paradedb.global_mutable_segment_rows",
		"paradedb.global_target_segment_size",
	})

	// Add ParadeDB-specific queries to capture
	pgDriver.SetExtraQueries([]postgres.ConfigQuery{
		{Key: "paradedb_version", Query: "SELECT paradedb.version_info()"},
	})

	return driver, nil
}
