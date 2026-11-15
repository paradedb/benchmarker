// Package elasticsearch registers the Elasticsearch driver.
package elasticsearch

import (
	"os"
	"strings"

	"github.com/paradedb/benchmarker/drivers"
	elastic "github.com/paradedb/benchmarker/drivers/shared/elasticsearch"
)

func init() {
	drivers.Register("elasticsearch", drivers.DriverConfig{
		Factory:  New,
		FileType: "json",
	})
}

// New creates a new Elasticsearch driver. API key credentials come only from
// ELASTICSEARCH_API_KEY and use Elasticsearch's encoded key format.
func New(connString string) (drivers.Driver, error) {
	return elastic.New(connString, elastic.DriverConfig{
		VersionInfoField: "build_flavor",
		APIKey:           strings.TrimSpace(os.Getenv("ELASTICSEARCH_API_KEY")),
	})
}
