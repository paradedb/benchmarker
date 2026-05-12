// Package postgres registers the PostgreSQL backend.
package postgres

import (
	"github.com/paradedb/benchmarker/backends"
	pgshared "github.com/paradedb/benchmarker/backends/shared/postgres"
)

func init() {
	backends.Register("postgres", backends.BackendConfig{
		Factory:     pgshared.New,
		FileType:    "sql",
		EnvVar:      "POSTGRES_URL",
		DefaultConn: "postgres://postgres:postgres@localhost:5433/benchmark",
		Container:   "postgres",
	})
}
