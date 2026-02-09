// Package pgtextsearch registers the pg_textsearch backend.
package pgtextsearch

import (
	"github.com/paradedb/benchmarks/backends"
	"github.com/paradedb/benchmarks/backends/shared/postgres"
)

func init() {
	backends.Register("pg-textsearch", backends.BackendConfig{
		Factory:     postgres.New,
		FileType:    "sql",
		EnvVar:      "PG_TEXTSEARCH_URL",
		DefaultConn: "postgres://postgres:postgres@localhost:5435/benchmark",
		Container:   "pg-textsearch",
	})
}
