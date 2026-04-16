// Package opensearch registers the OpenSearch backend.
package opensearch

import (
	"os"
	"strconv"
	"strings"

	"github.com/paradedb/benchmarks/backends"
	"github.com/paradedb/benchmarks/backends/shared/elastic"
)

const skipTLSVerifyEnv = "OPENSEARCH_SKIP_TLS_VERIFY"

func init() {
	backends.Register("opensearch", backends.BackendConfig{
		Factory:     New,
		FileType:    "json",
		EnvVar:      "OPENSEARCH_URL",
		DefaultConn: "http://localhost:9201",
		Container:   "opensearch",
	})
}

func driverConfig() elastic.DriverConfig {
	return elastic.DriverConfig{
		SkipTLSVerify:    envBool(skipTLSVerifyEnv),
		VersionInfoField: "distribution",
	}
}

func envBool(name string) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return false
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return enabled
}

// New creates a new OpenSearch driver.
func New(connString string) (backends.Driver, error) {
	return elastic.New(connString, driverConfig())
}
