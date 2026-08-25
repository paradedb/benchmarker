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
		{Key: "paradedb.version", Query: "SELECT paradedb.version_info()::text"},
		// Segment count of every BM25 index. Config capture runs at benchmark
		// setup, after the dataset load, so this is the run-start layout.
		{Key: "segments_at_run_start", Query: `
			SELECT 'paradedb.segments_at_run_start.' || c.relname,
			       (SELECT count(*) FROM paradedb.index_info(c.oid::regclass))::text
			FROM pg_class c
			JOIN pg_am am ON c.relam = am.oid
			WHERE am.amname = 'bm25'
			ORDER BY c.relname`},
	})

	return driver, nil
}
