// Package postgresfts registers the PostgreSQL full-text search backend.
package postgresfts

import (
	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/backends/shared/postgres"
)

func init() {
	backends.Register("postgresfts", backends.BackendConfig{
		Factory:     postgres.New,
		FileType:    "sql",
		EnvVar:      "POSTGRES_FTS_URL",
		DefaultConn: "postgres://postgres:postgres@localhost:5433/benchmark",
		Container:   "postgresfts",
	})
}
