package search

import (
	"context"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/metrics"
	"go.k6.io/k6/js/modules"

	// Import backends to register them via init()
	_ "github.com/paradedb/benchmarker/backends/clickhouse"
	_ "github.com/paradedb/benchmarker/backends/elasticsearch"
	_ "github.com/paradedb/benchmarker/backends/mongodb"
	_ "github.com/paradedb/benchmarker/backends/opensearch"
	_ "github.com/paradedb/benchmarker/backends/postgres"
)

// Backends holds all configured backend clients.
type Backends struct {
	vu            modules.VU
	ParadeDB      *backends.K6Client `js:"paradedb"`
	PostgresFts   *backends.K6Client `js:"postgresfts"`
	PgTextsearch  *backends.K6Client `js:"textsearch"`
	Elasticsearch *backends.K6Client `js:"elasticsearch"`
	OpenSearch    *backends.K6Client `js:"opensearch"`
	Clickhouse    *backends.K6Client `js:"clickhouse"`
	MongoDB       *backends.K6Client `js:"mongodb"`
	Metrics       *metrics.Collector `js:"metrics"`
}

// newBackends creates a new backends registry with the specified configuration.
func (m *ModuleInstance) newBackends(config map[string]interface{}) *Backends {
	b := &Backends{vu: m.vu}
	var enabledContainers []string
	ctx := context.Background()

	defaults := backends.DefaultConnections()
	containers := backends.DefaultContainers()

	for name, cfg := range config {
		alias := parseAlias(cfg)
		container := parseContainer(cfg, containers[name], alias)
		opts := backends.K6ClientOptions{Container: container, Alias: alias}
		conn := parseConn(cfg, defaults[name])

		driver, err := backends.NewDriver(name, conn)
		if err != nil {
			continue
		}

		client := backends.NewK6Client(m.vu, driver, name, opts)
		enabledContainers = append(enabledContainers, container)
		driver.CaptureConfig(ctx, name)

		// Assign to named fields for JS API compatibility
		switch name {
		case "paradedb":
			b.ParadeDB = client
		case "postgres-fts":
			b.PostgresFts = client
		case "pg-textsearch":
			b.PgTextsearch = client
		case "elasticsearch":
			b.Elasticsearch = client
		case "opensearch":
			b.OpenSearch = client
		case "clickhouse":
			b.Clickhouse = client
		case "mongodb":
			b.MongoDB = client
		}
	}

	// Auto-create metrics collector for enabled containers
	if len(enabledContainers) > 0 {
		containersInterface := make([]interface{}, len(enabledContainers))
		for i, c := range enabledContainers {
			containersInterface[i] = c
		}
		b.Metrics = metrics.NewCollector(m.vu, map[string]interface{}{
			"containers": containersInterface,
		})
	}

	return b
}

// parseConn extracts connection string from config.
func parseConn(cfg interface{}, defaultConn string) string {
	switch v := cfg.(type) {
	case bool:
		if v {
			return defaultConn
		}
		return ""
	case string:
		return v
	case map[string]interface{}:
		if c, ok := v["connection"].(string); ok {
			return c
		}
		if c, ok := v["address"].(string); ok {
			return c
		}
		return defaultConn
	default:
		return defaultConn
	}
}

// parseContainer extracts container name from config, or returns default.
// If alias is provided and container is not, container defaults to alias.
func parseContainer(cfg interface{}, defaultContainer string, alias string) string {
	if m, ok := cfg.(map[string]interface{}); ok {
		if c, ok := m["container"].(string); ok {
			return c
		}
	}
	// Default container to alias if alias is set
	if alias != "" {
		return alias
	}
	return defaultContainer
}

// parseAlias extracts alias name from config.
func parseAlias(cfg interface{}) string {
	if m, ok := cfg.(map[string]interface{}); ok {
		if a, ok := m["alias"].(string); ok {
			return a
		}
	}
	return ""
}

// Collect collects metrics from all enabled containers.
func (b *Backends) Collect() map[string]interface{} {
	if b.Metrics != nil {
		return b.Metrics.Collect()
	}
	return nil
}
