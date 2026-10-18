// Package elasticsearch registers the Elasticsearch driver.
package elasticsearch

import (
	"github.com/paradedb/benchmarker/drivers"
	elastic "github.com/paradedb/benchmarker/drivers/shared/elasticsearch"
)

func init() {
	drivers.Register("elasticsearch", drivers.DriverConfig{
		Factory:  New,
		FileType: "json",
	})
}

// New creates a new Elasticsearch driver.
func New(connString string) (drivers.Driver, error) {
	return elastic.New(connString, elastic.DriverConfig{
		VersionInfoField: "build_flavor",
	})
}
