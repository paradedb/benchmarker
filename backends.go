package search

import (
	"context"
	"fmt"
	"time"

	"github.com/paradedb/benchmarks/backends"
	"github.com/paradedb/benchmarks/metrics"
	"go.k6.io/k6/js/modules"

	// Import backends to register them via init()
	_ "github.com/paradedb/benchmarks/backends/clickhouse"
	_ "github.com/paradedb/benchmarks/backends/elasticsearch"
	_ "github.com/paradedb/benchmarks/backends/mongodb"
	_ "github.com/paradedb/benchmarks/backends/opensearch"
	_ "github.com/paradedb/benchmarks/backends/paradedb"
	_ "github.com/paradedb/benchmarks/backends/pgtextsearch"
	_ "github.com/paradedb/benchmarks/backends/postgresfts"
)

// Backends holds all configured backend clients.
type Backends struct {
	vu      modules.VU
	clients map[string]*backends.K6Client
	Metrics *metrics.Collector `js:"metrics"`
}

// Get returns a backend client by its alias/name.
func (b *Backends) Get(alias string) *backends.K6Client {
	return b.clients[alias]
}

// newBackends creates a new backends registry with the specified configuration.
func (m *ModuleInstance) newBackends(config map[string]interface{}) *Backends {
	b := &Backends{
		vu:      m.vu,
		clients: make(map[string]*backends.K6Client),
	}
	var enabledContainers []string
	ctx := context.Background()

	datasetPath := parseDatasetPath(config, m.vu)
	defaults := backends.DefaultConnections()
	defaultContainers := backends.DefaultContainers()

	// Parse backends array
	backendsArray, ok := config["backends"].([]interface{})
	if !ok {
		panic("backends: 'backends' array is required")
	}

	for _, item := range backendsArray {
		var backendType, alias, container, color, conn string

		switch v := item.(type) {
		case string:
			// Shorthand: just the backend type name
			backendType = v
			alias = v
		case map[string]interface{}:
			// Full config object
			var ok bool
			backendType, ok = v["type"].(string)
			if !ok {
				panic("backends: each backend config must have a 'type' field")
			}
			alias = backendType
			if a, ok := v["alias"].(string); ok {
				alias = a
			}
			if c, ok := v["container"].(string); ok {
				container = c
			}
			if c, ok := v["color"].(string); ok {
				color = c
			}
			if c, ok := v["connection"].(string); ok {
				conn = c
			}
		default:
			panic("backends: each backend must be a string or object")
		}

		// Validate backend type
		backendCfg, ok := backends.GetConfig(backendType)
		if !ok {
			panic(fmt.Sprintf("backends: unknown backend type '%s'. Valid types: %v",
				backendType, backends.RegisteredBackends()))
		}

		// Apply defaults
		if conn == "" {
			conn = defaults[backendType]
		}
		if container == "" {
			if alias != backendType {
				container = alias // default container to alias if alias is set
			} else {
				container = defaultContainers[backendType]
			}
		}

		// Check for duplicate alias
		if _, exists := b.clients[alias]; exists {
			panic(fmt.Sprintf("backends: duplicate alias '%s'", alias))
		}

		driver, err := backends.NewDriver(backendType, conn)
		if err != nil {
			panic(fmt.Sprintf("backends: failed to create '%s': %v", alias, err))
		}

		// Register backend options
		metrics.RegisterBackendOptions(alias, &metrics.BackendOptions{
			Container: container,
			Alias:     alias,
			Color:     color,
		})

		client := backends.NewK6Client(m.vu, driver, alias)
		b.clients[alias] = client
		enabledContainers = append(enabledContainers, container)

		driver.CaptureConfig(ctx, alias)
		metrics.CapturePrePostScripts(alias, backendType, datasetPath, backendCfg.FileType)
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

// Collect collects metrics from all enabled containers.
// Includes a 500ms sleep to avoid polling too frequently.
func (b *Backends) Collect() map[string]interface{} {
	if b.Metrics != nil {
		result := b.Metrics.Collect()
		time.Sleep(500 * time.Millisecond)
		return result
	}
	return nil
}

// Close closes all backend connections.
// Use this at the end of a test or between groups to clean up connections.
func (b *Backends) Close() {
	for _, client := range b.clients {
		client.Close()
	}
}

// SetTimeout sets the query timeout for all backends in seconds.
// Use 0 to disable timeout (default).
func (b *Backends) SetTimeout(seconds int) {
	for _, client := range b.clients {
		client.SetTimeout(seconds)
	}
}

// parseDatasetPath extracts dataset path from config.
// Defaults to "../" (parent of k6 script directory) and resolves relative to script location.
func parseDatasetPath(config map[string]interface{}, vu modules.VU) string {
	datasetPath := "../"
	if dp, ok := config["datasetPath"].(string); ok {
		datasetPath = dp
	}

	// Resolve relative to script location
	if vu != nil {
		if initEnv := vu.InitEnv(); initEnv != nil {
			datasetPath = initEnv.GetAbsFilePath(datasetPath)
		}
	}

	return datasetPath
}
