// Package dashboard provides a web-based dashboard for k6 search benchmarks.
package dashboard

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/paradedb/benchmarks/metrics"
	"go.k6.io/k6/output"
)

//go:embed static/*
var staticFiles embed.FS

func init() {
	output.RegisterExtension("dashboard", New)
}

// Output implements the k6 output.Output interface.
type Output struct {
	output.SampleBuffer

	params output.Params
	server *http.Server
	stopCh chan struct{}
	doneCh chan struct{}

	mu      sync.RWMutex
	clients map[chan []byte]struct{}

	// Accumulated data per run
	data *DashboardData
}

// DashboardData holds all metrics for the dashboard.
type DashboardData struct {
	StartTime     time.Time                    `json:"startTime"`
	TotalDuration float64                      `json:"totalDuration"` // Total test duration in seconds
	Runs          map[string]*RunMetrics       `json:"runs"`
	Containers    map[string]*ContainerMetrics `json:"-"` // Container metrics by container name (independent of runs)
}

// ContainerMetrics holds CPU/memory metrics for a container.
type ContainerMetrics struct {
	Name    string      `json:"name"`
	Backend string      `json:"backend"` // Associated backend name
	Alias   string      `json:"alias"`   // User-defined alias for display
	Color   string      `json:"color"`
	CPU     []TimeValue `json:"cpu"`
	Memory  []TimeValue `json:"memory"`
}

// RunMetrics holds metrics for a single run/phase.
type RunMetrics struct {
	Name           string                   `json:"name"`
	Backend        string                   `json:"backend"`   // Backend type: paradedb, elasticsearch, clickhouse, mongodb, etc.
	Container      string                   `json:"container"` // Docker container name for resource metrics
	Alias          string                   `json:"alias"`     // User-defined alias for this backend instance
	Color          string                   `json:"color"`     // Custom color for this backend
	Chart          string                   `json:"chart"`     // Chart group for separating graphs
	Latencies      []float64                `json:"latencies"`
	Timeline       []TimelinePoint          `json:"timeline"`
	IngestRate     []TimeValue              `json:"ingestRate"`    // Docs/sec timeline
	TotalIngested  int64                    `json:"totalIngested"` // Total docs ingested
	LastIngestTime int64                    `json:"-"`             // For rate calculation
	LastIngestDocs int64                    `json:"-"`             // For rate calculation
	Queries        map[string]*QueryMetrics `json:"-"`             // Per-query breakdown
	StartTime      int64                    `json:"startTime"`
	EndTime        int64                    `json:"endTime"`
	LastUpdateTime int64                    `json:"-"` // Track last update for end detection
}

// QueryMetrics holds metrics for a specific query type within a run.
type QueryMetrics struct {
	Name      string          `json:"name"`
	VUs       int             `json:"vus"`
	Executor  string          `json:"executor"`
	Latencies []float64       `json:"latencies"`
	HitCounts []int64         `json:"-"` // Raw hit counts for timeline calculation
	Timeline  []TimelinePoint `json:"timeline"`
}

// TimelinePoint is a point in time with aggregated metrics.
type TimelinePoint struct {
	Time  int64   `json:"time"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	Count int     `json:"count"`
	Hits  float64 `json:"hits"` // Average hits per query in this interval
}

// TimeValue is a timestamped value.
type TimeValue struct {
	Time  int64   `json:"time"`
	Value float64 `json:"value"`
}

// Constants for timing thresholds
const (
	runEndTimeoutMs      = 2000 // Time without updates before run is considered ended
	ingestRateIntervalMs = 500  // Minimum interval between ingest rate calculations
)

// getRunName computes the run identifier from backend, alias, and chart tag.
func getRunName(backend string, tags map[string]string) string {
	opts := metrics.GetBackendOptions(backend)
	run := backend
	if opts != nil && opts.Alias != "" {
		run = opts.Alias
	}
	if chart := tags["chart"]; chart != "" {
		run = run + " (" + chart + ")"
	}
	return run
}

// getOrCreateRun gets or creates a RunMetrics entry for the given run name.
func (o *Output) getOrCreateRun(runName, backend string, tags map[string]string) *RunMetrics {
	if o.data.Runs[runName] != nil {
		return o.data.Runs[runName]
	}

	opts := metrics.GetBackendOptions(backend)
	rm := &RunMetrics{
		Name:    runName,
		Backend: backend,
		Chart:   tags["chart"],
		Queries: make(map[string]*QueryMetrics),
	}
	if opts != nil {
		rm.Container = opts.Container
		rm.Alias = opts.Alias
		rm.Color = opts.Color
	}
	o.data.Runs[runName] = rm
	return rm
}

// New creates a new dashboard output.
func New(params output.Params) (output.Output, error) {
	return &Output{
		params:  params,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
		clients: make(map[chan []byte]struct{}),
		data: &DashboardData{
			StartTime:  time.Now(),
			Runs:       make(map[string]*RunMetrics),
			Containers: make(map[string]*ContainerMetrics),
		},
	}, nil
}

// Description returns a human-readable description.
func (o *Output) Description() string {
	return "Web Dashboard (http://localhost:5665)"
}

// Start starts the HTTP server.
func (o *Output) Start() error {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticFiles)))
	mux.HandleFunc("/events", o.handleSSE)
	mux.HandleFunc("/data", o.handleData)

	o.server = &http.Server{
		Addr:    ":5665",
		Handler: mux,
	}

	go func() {
		if err := o.server.ListenAndServe(); err != http.ErrServerClosed {
			fmt.Printf("Dashboard server error: %v\n", err)
		}
	}()

	go o.loop()

	fmt.Println("\n📊 Dashboard: http://localhost:5665/static/")

	return nil
}

// Stop shuts down the server and optionally saves results to JSON.
func (o *Output) Stop() error {
	close(o.stopCh)
	<-o.doneCh

	// Save dashboard state to JSON file if DASHBOARD_EXPORT=true
	if os.Getenv("DASHBOARD_EXPORT") == "true" {
		o.mu.RLock()
		data := o.getSummary()
		o.mu.RUnlock()

		jsonData, err := json.MarshalIndent(data, "", "  ")
		if err == nil {
			filename := fmt.Sprintf("dashboard_%s.json", time.Now().Format("2006-01-02_15-04-05"))
			if err := os.WriteFile(filename, jsonData, 0644); err == nil {
				fmt.Printf("\n📊 Dashboard results saved to: %s\n", filename)
				fmt.Printf("   View with: dashboard-viewer %s\n\n", filename)
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := o.server.Shutdown(ctx); err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}

func (o *Output) loop() {
	defer close(o.doneCh)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-o.stopCh:
			return
		case <-ticker.C:
			o.flush()
			o.broadcast()
		}
	}
}

func (o *Output) flush() {
	samples := o.GetBufferedSamples()
	if len(samples) == 0 {
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	now := time.Now().UnixMilli()

	for _, sc := range samples {
		for _, sample := range sc.GetSamples() {
			name := sample.Metric.Name
			value := sample.Value
			tags := sample.Tags.Map()

			switch {
			case name == "backend_init":
				// Backend initialization signal - register container for metrics
				backend := tags["backend"]
				if backend == "" {
					continue
				}

				// Register container for metrics
				opts := metrics.GetBackendOptions(backend)
				if opts != nil && opts.Container != "" {
					if o.data.Containers[opts.Container] == nil {
						o.data.Containers[opts.Container] = &ContainerMetrics{
							Name:    opts.Container,
							Backend: backend,
							Alias:   opts.Alias,
							Color:   opts.Color,
						}
					} else {
						// Update backend/color/alias info if container already exists
						o.data.Containers[opts.Container].Backend = backend
						o.data.Containers[opts.Container].Alias = opts.Alias
						o.data.Containers[opts.Container].Color = opts.Color
					}
				}

			case name == "scenario_started":
				// Scenario started signal - create run entry immediately
				// Query entries are created on demand when search_duration arrives
				backend := tags["backend"]
				if backend == "" {
					continue
				}

				runName := getRunName(backend, tags)
				rm := o.getOrCreateRun(runName, backend, tags)
				if rm.StartTime == 0 {
					rm.StartTime = sample.Time.UnixMilli()
				}

<<<<<<< Updated upstream
				// Create query entry for the scenario
				queryName := tags["scenario"]
				if queryName != "" && rm.Queries[queryName] == nil {
					rm.Queries[queryName] = &QueryMetrics{Name: queryName}
					if info := metrics.GetScenarioInfo(queryName); info != nil {
						rm.Queries[queryName].VUs = int(info.VUs)
						rm.Queries[queryName].Executor = info.Executor
					}
				}

=======
>>>>>>> Stashed changes
			case name == "search_duration":
				backend := tags["backend"]
				if backend == "" {
					backend = tags["run"]
				}
				if backend == "" {
					backend = tags["scenario"]
				}
				if backend == "" {
					continue
				}

				runName := getRunName(backend, tags)
				rm := o.getOrCreateRun(runName, backend, tags)
				rm.Latencies = append(rm.Latencies, value)
				if rm.StartTime == 0 {
					rm.StartTime = sample.Time.UnixMilli()
				}
				rm.LastUpdateTime = now

				// Track per-query metrics
				queryName := tags["query"]
				if queryName == "" {
					queryName = tags["scenario"]
				}
				if queryName != "" {
					if rm.Queries[queryName] == nil {
						rm.Queries[queryName] = &QueryMetrics{Name: queryName}
					}
					qm := rm.Queries[queryName]
					qm.Latencies = append(qm.Latencies, value)
					if qm.VUs == 0 || qm.Executor == "" {
						if info := metrics.GetScenarioInfo(queryName); info != nil {
							if qm.VUs == 0 {
								qm.VUs = int(info.VUs)
							}
							if qm.Executor == "" {
								qm.Executor = info.Executor
							}
						}
					}
				}

			case name == "search_hits":
				backend := tags["backend"]
				if backend == "" {
					backend = tags["run"]
				}
				if backend == "" {
					backend = tags["scenario"]
				}
				if backend == "" {
					continue
				}

				runName := getRunName(backend, tags)
				rm := o.data.Runs[runName]
				if rm == nil {
					continue // Run should already exist from search_duration
				}

				queryName := tags["query"]
				if queryName == "" {
					queryName = tags["scenario"]
				}
				if queryName != "" {
					if qm := rm.Queries[queryName]; qm != nil {
						qm.HitCounts = append(qm.HitCounts, int64(value))
					}
				}

			case name == "container_cpu_percent":
				container := tags["container"]
				if container == "" {
					continue
				}
				// Create container entry if it doesn't exist
				if o.data.Containers[container] == nil {
					o.data.Containers[container] = &ContainerMetrics{Name: container}
				}
				o.data.Containers[container].CPU = append(o.data.Containers[container].CPU, TimeValue{Time: sample.Time.UnixMilli(), Value: value})

			case name == "container_memory_bytes":
				container := tags["container"]
				if container == "" {
					continue
				}
				// Create container entry if it doesn't exist
				if o.data.Containers[container] == nil {
					o.data.Containers[container] = &ContainerMetrics{Name: container}
				}
				o.data.Containers[container].Memory = append(o.data.Containers[container].Memory, TimeValue{Time: sample.Time.UnixMilli(), Value: value})

			case name == "ingest_docs":
				backend := tags["backend"]
				if backend == "" {
					backend = tags["run"]
				}
				if backend == "" {
					backend = tags["scenario"]
				}
				if backend == "" {
					continue
				}

				runName := getRunName(backend, tags)
				rm := o.getOrCreateRun(runName, backend, tags)
				rm.TotalIngested += int64(value)
				if rm.StartTime == 0 {
					rm.StartTime = sample.Time.UnixMilli()
				}
				rm.LastUpdateTime = now
			}
		}
	}

	// Update timeline points for all runs
	for _, rm := range o.data.Runs {
		o.updateIngestRate(rm, now)
		// Update per-query timelines
		for _, qm := range rm.Queries {
			o.updateQueryTimeline(qm, rm.StartTime, now)
		}
	}
}

// updateQueryTimeline updates the timeline for a specific query.
func (o *Output) updateQueryTimeline(qm *QueryMetrics, runStartTime int64, now int64) {
	if len(qm.Latencies) == 0 {
		return
	}

	lastIdx := 0
	if len(qm.Timeline) > 0 {
		lastCount := 0
		for _, tp := range qm.Timeline {
			lastCount += tp.Count
		}
		lastIdx = lastCount
	}

	if lastIdx >= len(qm.Latencies) {
		return
	}

	recent := qm.Latencies[lastIdx:]
	if len(recent) == 0 {
		return
	}

	// Calculate average hits for this interval
	var avgHits float64
	if len(qm.HitCounts) > lastIdx {
		recentHits := qm.HitCounts[lastIdx:]
		if len(recentHits) > 0 {
			var sum int64
			for _, h := range recentHits {
				sum += h
			}
			avgHits = float64(sum) / float64(len(recentHits))
		}
	}

	qm.Timeline = append(qm.Timeline, TimelinePoint{
		Time:  now,
		P50:   percentile(recent, 50),
		P90:   percentile(recent, 90),
		P95:   percentile(recent, 95),
		P99:   percentile(recent, 99),
		Count: len(recent),
		Hits:  avgHits,
	})
}

// updateIngestRate calculates docs/sec for the last interval.
func (o *Output) updateIngestRate(rm *RunMetrics, now int64) {
	if rm.TotalIngested == 0 {
		return
	}

	// First ingest data point - add initial rate
	if rm.LastIngestTime == 0 {
		rm.LastIngestTime = now
		rm.LastIngestDocs = rm.TotalIngested
		rm.IngestRate = append(rm.IngestRate, TimeValue{Time: now, Value: float64(rm.TotalIngested)})
		return
	}

	// Calculate rate at ingestRateIntervalMs intervals
	elapsed := now - rm.LastIngestTime
	if elapsed < ingestRateIntervalMs {
		return
	}

	docsDelta := rm.TotalIngested - rm.LastIngestDocs
	rate := float64(docsDelta) / (float64(elapsed) / 1000.0) // docs per second

	rm.IngestRate = append(rm.IngestRate, TimeValue{Time: now, Value: rate})
	rm.LastIngestTime = now
	rm.LastIngestDocs = rm.TotalIngested
}

func (o *Output) broadcast() {
	o.mu.RLock()
	data, _ := json.Marshal(o.getSummary())
	o.mu.RUnlock()

	o.mu.Lock()
	for ch := range o.clients {
		select {
		case ch <- data:
		default:
		}
	}
	o.mu.Unlock()
}

func (o *Output) getSummary() map[string]interface{} {
	elapsed := time.Since(o.data.StartTime).Seconds()
	now := time.Now().UnixMilli()

	// Calculate max run duration (not overall time) for chart scaling
	var maxRunDuration float64
	for _, rm := range o.data.Runs {
		if rm.StartTime > 0 {
			var runDuration float64
			if rm.EndTime > 0 {
				runDuration = float64(rm.EndTime-rm.StartTime) / 1000
			} else {
				// Check query timelines for last data point
				var lastTime int64
				for _, qm := range rm.Queries {
					if len(qm.Timeline) > 0 {
						if t := qm.Timeline[len(qm.Timeline)-1].Time; t > lastTime {
							lastTime = t
						}
					}
				}
				if lastTime > 0 {
					runDuration = float64(lastTime-rm.StartTime) / 1000
				}
			}
			if runDuration > maxRunDuration {
				maxRunDuration = runDuration
			}
		}
	}
	// Use totalDuration if set, otherwise use max run duration + buffer
	chartDuration := o.data.TotalDuration
	if chartDuration == 0 && maxRunDuration > 0 {
		chartDuration = maxRunDuration + 5 // Add 5 second buffer
	}
	if chartDuration == 0 {
		chartDuration = 30 // Default fallback
	}

	runs := make(map[string]interface{})
	for name, rm := range o.data.Runs {
		// Check if run has ended (no updates for runEndTimeoutMs)
		if rm.EndTime == 0 && rm.LastUpdateTime > 0 && (now-rm.LastUpdateTime) > runEndTimeoutMs {
			rm.EndTime = rm.LastUpdateTime
		}

		// Calculate run duration (used for QPS calculations)
		var runDuration float64
		if rm.StartTime > 0 {
			if rm.EndTime > 0 {
				runDuration = float64(rm.EndTime-rm.StartTime) / 1000
			} else {
				runDuration = float64(now-rm.StartTime) / 1000
			}
		}

		// Calculate ingest rate (docs/sec) based on actual ingest duration
		var ingestRate float64
		if rm.TotalIngested > 0 && len(rm.IngestRate) > 0 {
			// Use time from first ingest to now/end for accurate rate
			ingestStart := rm.IngestRate[0].Time
			var ingestEnd int64
			if rm.EndTime > 0 {
				ingestEnd = rm.EndTime
			} else {
				ingestEnd = now
			}
			ingestDuration := float64(ingestEnd-ingestStart) / 1000
			if ingestDuration > 0 {
				ingestRate = float64(rm.TotalIngested) / ingestDuration
			}
		}

		// Build per-query stats
		queries := make(map[string]interface{})
		for qName, qm := range rm.Queries {
			// Get query pattern from any registered backend
			queryPattern := getQueryPattern(qName)

			// Calculate query-specific QPS using run duration
			var queryQPS float64
			if len(qm.Latencies) > 0 && runDuration > 0 {
				queryQPS = float64(len(qm.Latencies)) / runDuration
			}

			queries[qName] = map[string]interface{}{
				"name":     qm.Name,
				"vus":      qm.VUs,
				"executor": qm.Executor,
				"count":    len(qm.Latencies),
				"qps":      queryQPS,
				"min":      minVal(qm.Latencies),
				"max":      maxVal(qm.Latencies),
				"p50":      percentile(qm.Latencies, 50),
				"p95":      percentile(qm.Latencies, 95),
				"p99":      percentile(qm.Latencies, 99),
				"timeline": qm.Timeline,
				"query":    queryPattern,
			}
		}

		// Add database config based on backend tag
		config, containerLimits := getBackendConfig(rm.Backend, rm.Container)

		runs[name] = map[string]interface{}{
			"name":            rm.Name,
			"backend":         rm.Backend,
			"container":       rm.Container,
			"alias":           rm.Alias,
			"color":           rm.Color,
			"chart":           rm.Chart,
			"ingestRate":      rm.IngestRate,
			"totalIngested":   rm.TotalIngested,
			"avgIngestRate":   ingestRate,
			"queries":         queries,
			"startTime":       rm.StartTime,
			"config":          config,
			"containerLimits": containerLimits,
		}
	}

	// Build containers data
	containers := make(map[string]interface{})
	for name, cm := range o.data.Containers {
		containers[name] = map[string]interface{}{
			"name":    cm.Name,
			"backend": cm.Backend,
			"alias":   cm.Alias,
			"color":   cm.Color,
			"cpu":     cm.CPU,
			"memory":  cm.Memory,
		}
	}

	return map[string]interface{}{
		"elapsed":       elapsed,
		"chartDuration": chartDuration,
		"runs":          runs,
		"containers":    containers,
		"startTime":     o.data.StartTime.UnixMilli(),
	}
}

func (o *Output) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	ch := make(chan []byte, 10)

	o.mu.Lock()
	o.clients[ch] = struct{}{}
	o.mu.Unlock()

	defer func() {
		o.mu.Lock()
		delete(o.clients, ch)
		o.mu.Unlock()
		close(ch)
	}()

	o.mu.RLock()
	initial, _ := json.Marshal(o.getSummary())
	o.mu.RUnlock()
	fmt.Fprintf(w, "data: %s\n\n", initial)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (o *Output) handleData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	o.mu.RLock()
	defer o.mu.RUnlock()

	if err := json.NewEncoder(w).Encode(o.getSummary()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	idx := int(float64(len(sorted)-1) * p / 100)
	return sorted[idx]
}

func minVal(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	min := values[0]
	for _, v := range values[1:] {
		if v < min {
			min = v
		}
	}
	return min
}

func maxVal(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	max := values[0]
	for _, v := range values[1:] {
		if v > max {
			max = v
		}
	}
	return max
}

// getQueryPattern looks up a query pattern by scenario name.
func getQueryPattern(qName string) string {
	return metrics.GetQueryPattern(qName)
}

// getBackendConfig returns the database config and container limits for a backend type.
// Container limits are looked up by container name, not backend name.
func getBackendConfig(backend, container string) (map[string]interface{}, map[string]interface{}) {
	if backend == "" {
		return nil, nil
	}
	// Look up limits by container name (which may be alias or custom container name)
	limits := metrics.GetContainerLimits(container)
	if limits == nil && container != backend {
		// Fall back to backend name for backwards compatibility
		limits = metrics.GetContainerLimits(backend)
	}
	return metrics.GetBackendConfig(backend), limits
}

// ServeFile starts a server to view a saved dashboard JSON file.
// Optional notes parameter adds a notes section below the title.
func ServeFile(filename string, notes ...string) error {
	// Read the JSON file
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Parse and re-encode as compact JSON (SSE requires single-line data)
	var jsonData map[string]interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	// Add notes if provided
	if len(notes) > 0 && notes[0] != "" {
		jsonData["notes"] = notes[0]
	}

	compactData, _ := json.Marshal(jsonData)

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticFiles)))

	// Serve the static data
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// Send the compact JSON data once (SSE requires single-line format)
		fmt.Fprintf(w, "data: %s\n\n", compactData)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Keep connection open (browser expects SSE to stay open)
		<-r.Context().Done()
	})

	mux.HandleFunc("/data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if _, err := w.Write(compactData); err != nil {
			return
		}
	})

	server := &http.Server{
		Addr:    ":5665",
		Handler: mux,
	}

	fmt.Printf("\n📊 Viewing: %s\n", filename)
	fmt.Printf("   Chart: http://localhost:5665/static/\n")
	fmt.Printf("   Press Ctrl+C to exit\n\n")

	return server.ListenAndServe()
}

// ExportStandalone creates a standalone HTML file with embedded JSON data.
// Optional notes parameter adds a notes section below the title.
func ExportStandalone(jsonFile, outputFile string, notes ...string) error {
	// Read the JSON data
	jsonData, err := os.ReadFile(jsonFile)
	if err != nil {
		return fmt.Errorf("failed to read JSON file: %w", err)
	}

	// Validate and compact JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(jsonData, &parsed); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	// Add notes if provided
	if len(notes) > 0 && notes[0] != "" {
		parsed["notes"] = notes[0]
	}

	compactJSON, _ := json.Marshal(parsed)

	// Read the embedded HTML template
	htmlData, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		return fmt.Errorf("failed to read HTML template: %w", err)
	}

	// Replace the EventSource code with inline data
	oldCode := `const events = new EventSource("/events");
      events.onmessage = (e) => {
        try {
          update(JSON.parse(e.data));
        } catch (err) {
          console.error(err);
        }
      };`

	newCode := fmt.Sprintf(`// Standalone mode - embedded data
      const embeddedData = %s;
      update(embeddedData);`, string(compactJSON))

	html := strings.Replace(string(htmlData), oldCode, newCode, 1)

	// Write the output file
	if err := os.WriteFile(outputFile, []byte(html), 0644); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}

	return nil
}
