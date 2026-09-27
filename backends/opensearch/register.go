// Package opensearch registers the OpenSearch backend.
package opensearch

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/paradedb/benchmarker/backends"
	elastic "github.com/paradedb/benchmarker/backends/shared/elasticsearch"
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
	if envBool(skipTLSVerifyEnv) {
		return nil, fmt.Errorf("%s is no longer supported; trust the server CA in the system certificate store", skipTLSVerifyEnv)
	}
	return elastic.New(connString, driverConfig())
}
