// Package postgresfts registers the PostgreSQL full-text search backend.
package postgresfts

import (
	"github.com/paradedb/benchmarks/backends"
	"github.com/paradedb/benchmarks/backends/shared/postgres"
)

func init() {
	backends.Register("postgresfts", backends.BackendConfig{
		Factory:     postgres.New,
		FileType:    "sql",
		EnvVar:      "POSTGRESFTS_URL",
		DefaultConn: "postgres://postgres:postgres@localhost:5433/benchmark",
		Container:   "postgresfts",
	})
}
