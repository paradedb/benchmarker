// Package postgres registers the PostgreSQL backend.
package postgres

import (
	"github.com/paradedb/benchmarker/backends"
	pgshared "github.com/paradedb/benchmarker/backends/shared/postgres"
)

func init() {
	backends.Register("postgres", backends.BackendConfig{
		Factory:     New,
		FileType:    "sql",
		EnvVar:      "POSTGRES_URL",
		DefaultConn: "postgres://postgres:postgres@localhost:5433/benchmark",
		Container:   "postgres",
	})
}

// New enables standard PostgreSQL telemetry and index I/O accounting for the
// access methods used by native full-text and vector indexes.
func New(connString string) (backends.Driver, error) {
	driver, err := pgshared.New(connString)
	if err != nil {
		return nil, err
	}
	pgDriver := driver.(*pgshared.Driver)
	telemetryQueries := pgshared.StandardTelemetryQueries()
	telemetryQueries = append(telemetryQueries,
		pgshared.IndexIOTelemetryQueries("gin", "gist", "hnsw", "ivfflat")...)
	pgDriver.SetTelemetryQueries(telemetryQueries...)
	return driver, nil
}
