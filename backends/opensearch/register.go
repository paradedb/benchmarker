// Package opensearch registers the OpenSearch backend.
package opensearch

import (
	"github.com/paradedb/benchmarks/backends"
	"github.com/paradedb/benchmarks/backends/shared/elastic"
)

func init() {
	backends.Register("opensearch", backends.BackendConfig{
		Factory:     New,
		FileType:    "json",
		EnvVar:      "OPENSEARCH_URL",
		DefaultConn: "http://localhost:9201",
		Container:   "opensearch",
	})
}

// New creates a new OpenSearch driver.
func New(connString string) (backends.Driver, error) {
	return elastic.New(connString, elastic.DriverConfig{
		SkipTLSVerify:    true,
		VersionInfoField: "distribution",
	})
}
