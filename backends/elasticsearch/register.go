// Package elasticsearch registers the Elasticsearch backend.
package elasticsearch

import (
	"os"
	"strconv"
	"strings"

	"github.com/paradedb/benchmarker/backends"
	elastic "github.com/paradedb/benchmarker/backends/shared/elasticsearch"
)

const skipTLSVerifyEnv = "ELASTICSEARCH_SKIP_TLS_VERIFY"

func init() {
	backends.Register("elasticsearch", backends.BackendConfig{
		Factory:     New,
		FileType:    "json",
		EnvVar:      "ELASTICSEARCH_URL",
		DefaultConn: "https://elastic:elastic@localhost:9200",
		Container:   "elasticsearch",
	})
}

func driverConfig() elastic.DriverConfig {
	return elastic.DriverConfig{
		SkipTLSVerify:    envBool(skipTLSVerifyEnv, true),
		VersionInfoField: "build_flavor",
	}
}

func envBool(name string, defaultVal bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return defaultVal
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return defaultVal
	}
	return enabled
}

// New creates a new Elasticsearch driver.
func New(connString string) (backends.Driver, error) {
	return elastic.New(connString, driverConfig())
}
