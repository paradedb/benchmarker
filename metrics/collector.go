// Package metrics provides container metrics collection via Docker API.
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.k6.io/k6/js/modules"
	"go.k6.io/k6/metrics"
)

var (
	// Metrics registered once globally
	containerCPU    *metrics.Metric
	containerMemory *metrics.Metric
	metricsOnce     sync.Once

	// Container resource limits (captured once)
	ContainerLimits   = make(map[string]map[string]interface{})
	containerLimitsMu sync.RWMutex
	limitsCapture     = make(map[string]bool)

	// Backend configs (registered by each backend - database settings etc)
	backendConfigs   = make(map[string]map[string]interface{})
	backendConfigsMu sync.RWMutex

	// Backend options (registered once at init - container, alias, color)
	backendOptions   = make(map[string]*BackendOptions)
	backendOptionsMu sync.RWMutex
)

// BackendOptions holds user-specified options for a backend.
type BackendOptions struct {
	Container string
	Alias     string
	Color     string
}

// RegisterBackendConfig stores a backend's configuration for dashboard display.
// Called by backends when they capture their database config.
func RegisterBackendConfig(backend string, config map[string]interface{}) {
	backendConfigsMu.Lock()
	defer backendConfigsMu.Unlock()
	backendConfigs[backend] = config
}

// GetBackendConfig returns the registered config for a backend.
func GetBackendConfig(backend string) map[string]interface{} {
	backendConfigsMu.RLock()
	defer backendConfigsMu.RUnlock()
	config := backendConfigs[backend]
	if config == nil {
		return nil
	}
	result := make(map[string]interface{}, len(config))
	for k, v := range config {
		result[k] = v
	}
	return result
}

// GetContainerLimits returns captured container limits by container name.
func GetContainerLimits(container string) map[string]interface{} {
	containerLimitsMu.RLock()
	defer containerLimitsMu.RUnlock()

	limits := ContainerLimits[container]
	if limits == nil {
		return nil
	}

	result := make(map[string]interface{}, len(limits))
	for k, v := range limits {
		result[k] = v
	}
	return result
}

// CapturePrePostScripts reads pre/post scripts from the dataset directory
// and adds them to the backend's config. The alias is used as the config key,
// while backendType is used for the directory name. The fileType should be "sql" or "json".
func CapturePrePostScripts(alias, backendType, datasetPath, fileType string) {
	if datasetPath == "" {
		return
	}

	backendConfigsMu.Lock()
	defer backendConfigsMu.Unlock()

	config := backendConfigs[alias]
	if config == nil {
		config = make(map[string]interface{})
		backendConfigs[alias] = config
	}

	preFile := filepath.Join(datasetPath, backendType, "pre."+fileType)
	if data, err := os.ReadFile(preFile); err == nil {
		config["pre_script"] = string(data)
	}

	postFile := filepath.Join(datasetPath, backendType, "post."+fileType)
	if data, err := os.ReadFile(postFile); err == nil {
		config["post_script"] = string(data)
	}
}

// RegisterBackendOptions stores user-specified options for a backend.
// Called once at init time from backends.go.
func RegisterBackendOptions(backend string, opts *BackendOptions) {
	backendOptionsMu.Lock()
	defer backendOptionsMu.Unlock()
	backendOptions[backend] = opts
}

// GetBackendOptions returns the registered options for a backend.
func GetBackendOptions(backend string) *BackendOptions {
	backendOptionsMu.RLock()
	defer backendOptionsMu.RUnlock()
	return backendOptions[backend]
}

// GetAllBackendOptions returns all registered backend options.
func GetAllBackendOptions() map[string]*BackendOptions {
	backendOptionsMu.RLock()
	defer backendOptionsMu.RUnlock()
	// Return a copy to avoid race conditions
	result := make(map[string]*BackendOptions)
	for k, v := range backendOptions {
		result[k] = v
	}
	return result
}

// Collector collects container metrics via Docker API.
type Collector struct {
	vu         modules.VU
	containers []string

	// HTTP client for Docker API
	httpClient *http.Client

	// Previous stats for CPU delta calculation (shared across calls)
	prevStats map[string]*rawDockerStats
	statsMu   sync.Mutex
}

// ContainerStats holds calculated container stats.
type ContainerStats struct {
	CPUPercent  float64
	MemoryBytes float64
}

// rawDockerStats holds raw Docker API stats for delta calculation.
type rawDockerStats struct {
	CPUTotal    uint64
	SystemCPU   uint64
	OnlineCPUs  int
	MemoryUsage uint64
	MemoryCache uint64
}

// dockerStats matches the Docker API stats response (partial).
type dockerStats struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Stats struct {
			Cache uint64 `json:"cache"`
		} `json:"stats"`
	} `json:"memory_stats"`
}

// NewCollector creates a new metrics collector.
func NewCollector(vu modules.VU, config map[string]interface{}) *Collector {
	// Register metrics once during init phase.
	if vu != nil && vu.InitEnv() != nil {
		metricsOnce.Do(func() {
			registry := vu.InitEnv().Registry
			containerCPU, _ = registry.NewMetric("container_cpu_percent", metrics.Gauge)
			containerMemory, _ = registry.NewMetric("container_memory_bytes", metrics.Gauge)
		})
	}

	var containers []string
	if c, ok := config["containers"].([]interface{}); ok {
		for _, name := range c {
			if s, ok := name.(string); ok {
				containers = append(containers, s)
			}
		}
	}

	// Create HTTP client for Docker socket with short timeout
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", "/var/run/docker.sock")
			},
		},
		Timeout: 2 * time.Second,
	}

	return &Collector{
		vu:         vu,
		containers: containers,
		prevStats:  make(map[string]*rawDockerStats),
		httpClient: httpClient,
	}
}

// Start is a no-op for compatibility (collection happens in Collect).
func (c *Collector) Start() map[string]interface{} {
	return map[string]interface{}{"status": "ready"}
}

// Stop is a no-op for compatibility.
func (c *Collector) Stop() map[string]interface{} {
	return map[string]interface{}{"status": "stopped"}
}

// containerResult holds the result of fetching stats for a single container.
type containerResult struct {
	container string
	stats     *ContainerStats
	err       error
}

// Collect fetches current stats and pushes them to k6 metrics.
// Call this from JavaScript in a loop with sleep.
func (c *Collector) Collect() map[string]interface{} {
	state := c.vu.State()
	if state == nil || containerCPU == nil || containerMemory == nil {
		return map[string]interface{}{"error": "not in VU context"}
	}

	ctx := c.vu.Context()
	if ctx == nil {
		return map[string]interface{}{"error": "no context"}
	}

	now := time.Now()
	baseTags := state.Tags.GetCurrentValues()
	results := make(map[string]interface{})

	// Capture container limits once (in parallel)
	var limitsWg sync.WaitGroup
	for _, container := range c.containers {
		containerLimitsMu.Lock()
		needsCapture := !limitsCapture[container]
		containerLimitsMu.Unlock()

		if needsCapture {
			limitsWg.Add(1)
			go func(cont string) {
				defer limitsWg.Done()
				if c.captureContainerLimits(cont) {
					containerLimitsMu.Lock()
					limitsCapture[cont] = true
					containerLimitsMu.Unlock()
				}
			}(container)
		}
	}
	limitsWg.Wait()

	// Fetch stats for all containers in parallel
	resultChan := make(chan containerResult, len(c.containers))
	for _, container := range c.containers {
		go func(cont string) {
			stats, err := c.fetchAndCalculateStats(cont)
			resultChan <- containerResult{container: cont, stats: stats, err: err}
		}(container)
	}

	// Collect results
	for range c.containers {
		res := <-resultChan
		if res.err != nil {
			results[res.container] = map[string]interface{}{"error": res.err.Error()}
			continue
		}

		// Push to k6 metrics
		tags := baseTags.Tags.With("container", res.container)

		metrics.PushIfNotDone(ctx, state.Samples, metrics.Sample{
			TimeSeries: metrics.TimeSeries{Metric: containerCPU, Tags: tags},
			Time:       now,
			Value:      res.stats.CPUPercent,
		})
		metrics.PushIfNotDone(ctx, state.Samples, metrics.Sample{
			TimeSeries: metrics.TimeSeries{Metric: containerMemory, Tags: tags},
			Time:       now,
			Value:      res.stats.MemoryBytes,
		})

		results[res.container] = map[string]interface{}{
			"cpu":    res.stats.CPUPercent,
			"memory": res.stats.MemoryBytes,
		}
	}

	return results
}

// fetchAndCalculateStats gets raw stats from Docker API and calculates CPU percentage.
func (c *Collector) fetchAndCalculateStats(container string) (*ContainerStats, error) {
	url := fmt.Sprintf("http://localhost/containers/%s/stats?stream=true", container)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("docker API error %d: %s", resp.StatusCode, string(body))
	}

	decoder := json.NewDecoder(resp.Body)
	var stats dockerStats
	if err := decoder.Decode(&stats); err != nil {
		return nil, err
	}

	current := &rawDockerStats{
		CPUTotal:    stats.CPUStats.CPUUsage.TotalUsage,
		SystemCPU:   stats.CPUStats.SystemCPUUsage,
		OnlineCPUs:  stats.CPUStats.OnlineCPUs,
		MemoryUsage: stats.MemoryStats.Usage,
		MemoryCache: stats.MemoryStats.Stats.Cache,
	}

	c.statsMu.Lock()
	prev := c.prevStats[container]
	c.prevStats[container] = current
	c.statsMu.Unlock()

	cpuPercent := 0.0
	if prev != nil && current.SystemCPU > prev.SystemCPU {
		cpuDelta := float64(current.CPUTotal - prev.CPUTotal)
		systemDelta := float64(current.SystemCPU - prev.SystemCPU)
		if systemDelta > 0 && cpuDelta > 0 {
			cpuPercent = (cpuDelta / systemDelta) * float64(current.OnlineCPUs) * 100.0
		}
	}

	memoryBytes := float64(current.MemoryUsage)
	if current.MemoryCache > 0 && current.MemoryUsage > current.MemoryCache {
		memoryBytes = float64(current.MemoryUsage - current.MemoryCache)
	}

	return &ContainerStats{
		CPUPercent:  cpuPercent,
		MemoryBytes: memoryBytes,
	}, nil
}

// dockerInspect matches the Docker API inspect response (partial).
type dockerInspect struct {
	HostConfig struct {
		NanoCPUs int64 `json:"NanoCpus"`
		Memory   int64 `json:"Memory"`
		CPUQuota int64 `json:"CpuQuota"`
	} `json:"HostConfig"`
}

// captureContainerLimits fetches container resource limits from Docker API.
func (c *Collector) captureContainerLimits(container string) bool {
	url := fmt.Sprintf("http://localhost/containers/%s/json", container)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	var inspect dockerInspect
	if err := json.NewDecoder(resp.Body).Decode(&inspect); err != nil {
		return false
	}

	limits := make(map[string]interface{})

	// CPU limit (NanoCPUs is CPU limit * 1e9, or use CpuQuota/100000)
	if inspect.HostConfig.NanoCPUs > 0 {
		cpuLimit := float64(inspect.HostConfig.NanoCPUs) / 1e9
		limits["cpu_limit"] = fmt.Sprintf("%.1f cores", cpuLimit)
	} else if inspect.HostConfig.CPUQuota > 0 {
		cpuLimit := float64(inspect.HostConfig.CPUQuota) / 100000
		limits["cpu_limit"] = fmt.Sprintf("%.1f cores", cpuLimit)
	}

	// Memory limit
	if inspect.HostConfig.Memory > 0 {
		memGB := float64(inspect.HostConfig.Memory) / (1024 * 1024 * 1024)
		limits["memory_limit"] = fmt.Sprintf("%.1f GB", memGB)
	}

	if len(limits) > 0 {
		containerLimitsMu.Lock()
		ContainerLimits[container] = limits
		containerLimitsMu.Unlock()
		return true
	}
	return false
}
