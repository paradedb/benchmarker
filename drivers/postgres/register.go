// Package postgres registers the PostgreSQL driver.
package postgres

import (
	"github.com/paradedb/benchmarker/drivers"
	pgshared "github.com/paradedb/benchmarker/drivers/shared/postgres"
)

func init() {
	drivers.Register("postgres", drivers.DriverConfig{
		Factory:  New,
		FileType: "sql",
	})
}

// New enables standard PostgreSQL telemetry and index I/O accounting for the
// access methods used by native full-text and vector indexes.
func New(connString string) (drivers.Driver, error) {
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
