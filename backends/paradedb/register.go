// Package paradedb registers the ParadeDB backend.
package paradedb

import (
	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/backends/shared/postgres"
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

	// Capture every paradedb.* GUC rather than a hardcoded list, so new or
	// renamed GUCs show up without code changes
	pgDriver.SetExtraGUCPrefixes([]string{"paradedb"})

	// Add ParadeDB-specific queries to capture
	pgDriver.SetExtraQueries([]postgres.ConfigQuery{
		// version_info() returns a composite record; cast so it scans as text
		{Key: "paradedb_version", Query: "SELECT paradedb.version_info()::text"},
	})

	return driver, nil
}
