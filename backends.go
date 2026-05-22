package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/grafana/sobek"
	"github.com/paradedb/benchmarker/backends"
	"github.com/paradedb/benchmarker/metrics"
	"go.k6.io/k6/js/common"
	"go.k6.io/k6/js/modules"

	// Import backends to register them via init()
	_ "github.com/paradedb/benchmarker/backends/clickhouse"
	_ "github.com/paradedb/benchmarker/backends/elasticsearch"
	_ "github.com/paradedb/benchmarker/backends/mongodb"
	_ "github.com/paradedb/benchmarker/backends/opensearch"
	_ "github.com/paradedb/benchmarker/backends/paradedb"
	_ "github.com/paradedb/benchmarker/backends/postgres"
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

// GetAll returns a list of backend names.
func (b *Backends) GetAll() []string {
	names := make([]string, 0, len(b.clients))
	for name := range b.clients {
		names = append(names, name)
	}
	return names
}

func (m *ModuleInstance) configErrorf(format string, args ...interface{}) {
	err := fmt.Errorf(format, args...)
	if m.vu != nil {
		if rt := m.vu.Runtime(); rt != nil {
			common.Throw(rt, err)
			return
		}
	}
	panic(err.Error())
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

	// Capture dataset.yaml (written by `loader pull`) if present.
	if datasetPath != "" {
		if data, err := os.ReadFile(filepath.Join(datasetPath, "dataset.yaml")); err == nil {
			metrics.RegisterRunCapture("dataset_yaml", string(data))
		}
	}

	// Capture the running k6 script's source. k6 is invoked as
	// `./k6 run [flags] <script.js>` — the script path is in os.Args.
	if src, path := readRunningScript(); src != "" {
		metrics.RegisterRunCapture("script", src)
		metrics.RegisterRunCapture("script_path", path)
	}

	// Parse backends array
	backendsArray, ok := config["backends"].([]interface{})
	if !ok {
		m.configErrorf("backends: 'backends' array is required")
		return nil
	}

	for _, item := range backendsArray {
		var backendType, alias, container, color, conn string
		// containerExplicit tracks whether the user set the container field at all
		// (including to ""). An explicit empty string opts the backend out of
		// docker metrics — used for off-host services like AWS RDS.
		var containerExplicit bool

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
				m.configErrorf("backends: each backend config must have a 'type' field")
				return nil
			}
			alias = backendType
			if a, ok := v["alias"].(string); ok {
				alias = a
			}
			if raw, ok := v["container"]; ok {
				containerExplicit = true
				if c, ok := raw.(string); ok {
					container = c
				}
			}
			if c, ok := v["color"].(string); ok {
				color = c
			}
			if c, ok := v["connection"].(string); ok {
				conn = c
			}
		default:
			m.configErrorf("backends: each backend must be a string or object")
			return nil
		}

		// Validate backend type
		backendCfg, ok := backends.GetConfig(backendType)
		if !ok {
			m.configErrorf("backends: unknown backend type '%s'. Valid types: %v",
				backendType, backends.RegisteredBackends())
			return nil
		}

		// Apply defaults
		if conn == "" {
			conn = defaults[backendType]
		}
		// Only default the container name if the user didn't explicitly set it.
		// An explicit "" opts out of docker capture (off-host backends).
		if !containerExplicit {
			if alias != backendType {
				container = alias // default container to alias if alias is set
			} else {
				container = defaultContainers[backendType]
			}
		}

		// Check for duplicate alias
		if _, exists := b.clients[alias]; exists {
			m.configErrorf("backends: duplicate alias '%s'", alias)
			return nil
		}

		driver, err := backends.NewDriver(backendType, conn)
		if err != nil {
			m.configErrorf("backends: failed to create '%s': %v", alias, err)
			return nil
		}

		// Register backend options
		metrics.RegisterBackendOptions(alias, &metrics.BackendOptions{
			Container: container,
			Alias:     alias,
			Color:     color,
		})

		client := backends.NewK6Client(m.vu, driver, alias)
		b.clients[alias] = client
		if container != "" {
			enabledContainers = append(enabledContainers, container)
		}

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

// AddDockerMetricsCollector adds a metrics_collector scenario to the given
// scenarios object. Pass a Timer or a duration string (e.g. "500s").
// Returns a function that the script should export as collectMetrics:
//
//	export const collectMetrics = backends.addDockerMetricsCollector(scenarios, timer);
func (b *Backends) AddDockerMetricsCollector(call sobek.FunctionCall) sobek.Value {
	rt := b.vu.Runtime()

	if len(call.Arguments) < 2 {
		common.Throw(rt, fmt.Errorf("addDockerMetricsCollector requires (scenarios, timer|duration)"))
		return sobek.Undefined()
	}

	scenarios := call.Arguments[0].ToObject(rt)

	var dur string
	durationArg := call.Arguments[1].Export()
	switch v := durationArg.(type) {
	case *Timer:
		dur = v.TotalDuration()
	case string:
		dur = v
	default:
		// Timer comes through as a wrapped Go object — try unwrapping
		if timer, ok := durationArg.(*Timer); ok {
			dur = timer.TotalDuration()
		} else {
			dur = call.Arguments[1].String()
		}
	}

	if err := scenarios.Set("metrics_collector", rt.ToValue(map[string]interface{}{
		"executor": "constant-vus",
		"vus":      1,
		"duration": dur,
		"exec":     "collectMetrics",
	})); err != nil {
		common.Throw(rt, fmt.Errorf("failed to set metrics_collector scenario: %w", err))
	}

	return rt.ToValue(func() map[string]interface{} {
		return b.Collect()
	})
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

// readRunningScript locates the currently-running k6 script via os.Args and
// returns its source text plus absolute path. The xk6 extension runs inside
// the k6 process so the script path is reachable from argv directly; k6's
// public extension API doesn't expose it as cleanly. Returns ("", "") if no
// candidate is found.
func readRunningScript() (source, path string) {
	for _, arg := range os.Args[1:] {
		if !looksLikeScript(arg) {
			continue
		}
		abs, err := filepath.Abs(arg)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		return string(data), abs
	}
	return "", ""
}

func looksLikeScript(arg string) bool {
	switch filepath.Ext(arg) {
	case ".js", ".ts", ".mjs", ".cjs":
		return true
	}
	return false
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
