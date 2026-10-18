// Package opensearch registers the OpenSearch driver.
package opensearch

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/paradedb/benchmarker/drivers"
	elastic "github.com/paradedb/benchmarker/drivers/shared/elasticsearch"
)

const skipTLSVerifyEnv = "OPENSEARCH_SKIP_TLS_VERIFY"

func init() {
	drivers.Register("opensearch", drivers.DriverConfig{
		Factory:  New,
		FileType: "json",
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
func New(connString string) (drivers.Driver, error) {
	if envBool(skipTLSVerifyEnv) {
		return nil, fmt.Errorf("%s is no longer supported; trust the server CA in the system certificate store", skipTLSVerifyEnv)
	}
	return elastic.New(connString, driverConfig())
}
