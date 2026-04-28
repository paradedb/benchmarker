// Package elasticsearch registers the Elasticsearch backend.
package elasticsearch

import (
	"github.com/paradedb/benchmarker/backends"
	elastic "github.com/paradedb/benchmarker/backends/shared/elasticsearch"
)

func init() {
	backends.Register("elasticsearch", backends.BackendConfig{
		Factory:     New,
		FileType:    "json",
		EnvVar:      "ELASTICSEARCH_URL",
		DefaultConn: "http://localhost:9200",
		Container:   "elasticsearch",
	})
}

// New creates a new Elasticsearch driver.
func New(connString string) (backends.Driver, error) {
	return elastic.New(connString, elastic.DriverConfig{
		SkipTLSVerify:    false,
		VersionInfoField: "build_flavor",
	})
}
