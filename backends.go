package search

import (
	"context"

	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/backends/clickhouse"
	"github.com/paradedb/benchmarker/backends/elasticsearch"
	"github.com/paradedb/benchmarker/backends/mongodb"
	"github.com/paradedb/benchmarker/backends/postgres"
	"github.com/paradedb/benchmarker/metrics"
	"go.k6.io/k6/js/modules"
)

// Backends holds all configured backend clients.
type Backends struct {
	vu         modules.VU
	Paradedb   *backends.K6Client `js:"paradedb"`
	PgFTS      *backends.K6Client `js:"postgresFts"`
	Textsearch *backends.K6Client `js:"textsearch"`
	Elastic    *backends.K6Client `js:"elasticsearch"`
	Click      *backends.K6Client `js:"clickhouse"`
	Mongo      *backends.K6Client `js:"mongodb"`
	Metrics    *metrics.Collector `js:"metrics"`
}

// newBackends creates a new backends registry with the specified configuration.
func (m *ModuleInstance) newBackends(config map[string]interface{}) *Backends {
	b := &Backends{vu: m.vu}
	var enabledContainers []string
	ctx := context.Background()

	defaults := backends.DefaultConnections()
	containers := backends.DefaultContainers()

	for name, cfg := range config {
		switch name {
		case "paradedb":
			conn := parseConn(cfg, defaults["paradedb"])
			if driver, err := postgres.New(conn); err == nil {
				b.Paradedb = backends.NewK6Client(m.vu, driver, "paradedb")
				enabledContainers = append(enabledContainers, parseContainer(cfg, containers["paradedb"]))
				driver.CaptureConfig(ctx, "paradedb")
			}

		case "postgres-fts":
			conn := parseConn(cfg, defaults["postgres-fts"])
			if driver, err := postgres.New(conn); err == nil {
				b.PgFTS = backends.NewK6Client(m.vu, driver, "postgres-fts")
				enabledContainers = append(enabledContainers, parseContainer(cfg, containers["postgres-fts"]))
				driver.CaptureConfig(ctx, "postgres-fts")
			}

		case "pg-textsearch":
			conn := parseConn(cfg, defaults["pg-textsearch"])
			if driver, err := postgres.New(conn); err == nil {
				b.Textsearch = backends.NewK6Client(m.vu, driver, "pg-textsearch")
				enabledContainers = append(enabledContainers, parseContainer(cfg, containers["pg-textsearch"]))
				driver.CaptureConfig(ctx, "pg-textsearch")
			}

		case "elasticsearch":
			conn := parseConn(cfg, defaults["elasticsearch"])
			if driver, err := elasticsearch.New(conn); err == nil {
				b.Elastic = backends.NewK6Client(m.vu, driver, "elasticsearch")
				enabledContainers = append(enabledContainers, parseContainer(cfg, containers["elasticsearch"]))
				driver.CaptureConfig(ctx, "elasticsearch")
			}

		case "clickhouse":
			conn := parseConn(cfg, defaults["clickhouse"])
			if driver, err := clickhouse.New(conn); err == nil {
				b.Click = backends.NewK6Client(m.vu, driver, "clickhouse")
				enabledContainers = append(enabledContainers, parseContainer(cfg, containers["clickhouse"]))
				driver.CaptureConfig(ctx, "clickhouse")
			}

		case "mongodb":
			conn := parseConn(cfg, defaults["mongodb"])
			if driver, err := mongodb.New(conn); err == nil {
				b.Mongo = backends.NewK6Client(m.vu, driver, "mongodb")
				enabledContainers = append(enabledContainers, parseContainer(cfg, containers["mongodb"]))
				driver.CaptureConfig(ctx, "mongodb")
			}
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
func parseContainer(cfg interface{}, defaultContainer string) string {
	if m, ok := cfg.(map[string]interface{}); ok {
		if c, ok := m["container"].(string); ok {
			return c
		}
	}
	return defaultContainer
}

// Collect collects metrics from all enabled containers.
func (b *Backends) Collect() map[string]interface{} {
	if b.Metrics != nil {
		return b.Metrics.Collect()
	}
	return nil
}
