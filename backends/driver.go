// Package backends provides the driver interface and shared infrastructure for search backends.
package backends

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jamesblackwood-sewell/xk6-search/metrics"
	"go.k6.io/k6/js/modules"
)

// Schema defines the dataset schema from schema.yaml
type Schema struct {
	Columns    map[string]string `yaml:"columns"`
	PrimaryKey string            `yaml:"primaryKey"`
	Table      string            `yaml:"table"`
	Index      string            `yaml:"index"`
	Collection string            `yaml:"collection"`
	Database   string            `yaml:"database"`
}

// BackendConfig holds all configuration for a backend.
type BackendConfig struct {
	Factory     DriverFactory
	FileType    string // "sql" or "json"
	EnvVar      string
	DefaultConn string
	Container   string
}

// Backend registry
var (
	backendConfigs   = make(map[string]BackendConfig)
	backendConfigsMu sync.RWMutex
)

// Register registers a backend with all its configuration.
func Register(name string, config BackendConfig) {
	backendConfigsMu.Lock()
	defer backendConfigsMu.Unlock()
	backendConfigs[name] = config
}

// GetConfig returns the configuration for a backend.
func GetConfig(name string) (BackendConfig, bool) {
	backendConfigsMu.RLock()
	defer backendConfigsMu.RUnlock()
	cfg, ok := backendConfigs[name]
	return cfg, ok
}

// NewDriver creates a driver for the named backend.
func NewDriver(name, connString string) (Driver, error) {
	backendConfigsMu.RLock()
	cfg, ok := backendConfigs[name]
	backendConfigsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown backend: %s", name)
	}
	return cfg.Factory(connString)
}

// DefaultConnections returns a map of backend names to default connection strings.
func DefaultConnections() map[string]string {
	backendConfigsMu.RLock()
	defer backendConfigsMu.RUnlock()
	m := make(map[string]string, len(backendConfigs))
	for name, cfg := range backendConfigs {
		m[name] = cfg.DefaultConn
	}
	return m
}

// ConnectionEnvVars returns a map of backend names to environment variable names.
func ConnectionEnvVars() map[string]string {
	backendConfigsMu.RLock()
	defer backendConfigsMu.RUnlock()
	m := make(map[string]string, len(backendConfigs))
	for name, cfg := range backendConfigs {
		m[name] = cfg.EnvVar
	}
	return m
}

// DefaultContainers returns a map of backend names to Docker container names.
func DefaultContainers() map[string]string {
	backendConfigsMu.RLock()
	defer backendConfigsMu.RUnlock()
	m := make(map[string]string, len(backendConfigs))
	for name, cfg := range backendConfigs {
		m[name] = cfg.Container
	}
	return m
}

// GetCLILoader creates a CLI loader for the named backend.
func GetCLILoader(name, connString string) *CLILoader {
	backendConfigsMu.RLock()
	cfg, ok := backendConfigs[name]
	backendConfigsMu.RUnlock()
	if !ok {
		return nil
	}
	return NewCLILoader(name, cfg.FileType, connString, cfg.Factory)
}

// GetAllCLILoaders creates CLI loaders for all registered backends.
func GetAllCLILoaders(getConnString func(name string) string) []*CLILoader {
	backendConfigsMu.RLock()
	defer backendConfigsMu.RUnlock()

	var loaders []*CLILoader
	for name, cfg := range backendConfigs {
		loaders = append(loaders, NewCLILoader(name, cfg.FileType, getConnString(name), cfg.Factory))
	}
	return loaders
}

// RegisteredBackends returns the names of all registered backends.
func RegisteredBackends() []string {
	backendConfigsMu.RLock()
	defer backendConfigsMu.RUnlock()

	names := make([]string, 0, len(backendConfigs))
	for name := range backendConfigs {
		names = append(names, name)
	}
	return names
}

// Driver is the minimal interface each backend must implement.
type Driver interface {
	Close() error
	Exec(ctx context.Context, statements string) error
	Query(ctx context.Context, query string, args ...any) (hitCount int, err error)
	Insert(ctx context.Context, table string, cols []string, rows [][]any) (int, error)
	CaptureConfig(ctx context.Context, backendName string)
}

// DriverFactory creates a driver from a connection string.
type DriverFactory func(connString string) (Driver, error)

// K6Client wraps a Driver with k6 metrics emission.
type K6Client struct {
	driver  Driver
	vu      modules.VU
	backend string
}

// NewK6Client creates a k6 client that wraps a driver.
func NewK6Client(vu modules.VU, driver Driver, backend string) *K6Client {
	metrics.RegisterMetrics(vu)
	return &K6Client{driver: driver, vu: vu, backend: backend}
}

// Search executes a query and emits metrics.
func (c *K6Client) Search(query string, args ...any) map[string]interface{} {
	ctx := context.Background()
	metrics.CaptureQueryPattern(c.vu, strings.TrimSpace(query))

	start := time.Now()
	hits, err := c.driver.Query(ctx, query, args...)
	latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

	if err != nil {
		return map[string]interface{}{
			"hits":      0,
			"latencyMs": latencyMs,
			"error":     err.Error(),
		}
	}

	result := &metrics.SearchResult{Hits: int64(hits), LatencyMs: latencyMs}
	result.Emit(ctx, c.vu, c.backend)
	return result.ToMap()
}

// InsertBatch inserts documents and emits metrics.
func (c *K6Client) InsertBatch(table string, docs []map[string]interface{}) map[string]interface{} {
	if len(docs) == 0 {
		return map[string]interface{}{"rows": 0, "latencyMs": 0.0}
	}

	// Extract columns from first doc
	var cols []string
	for col := range docs[0] {
		cols = append(cols, col)
	}

	// Convert docs to rows
	rows := make([][]any, len(docs))
	for i, doc := range docs {
		row := make([]any, len(cols))
		for j, col := range cols {
			row[j] = doc[col]
		}
		rows[i] = row
	}

	ctx := context.Background()
	start := time.Now()
	count, err := c.driver.Insert(ctx, table, cols, rows)
	latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

	if err != nil {
		return map[string]interface{}{
			"rows":      0,
			"latencyMs": latencyMs,
			"error":     err.Error(),
		}
	}

	result := &metrics.IngestResult{Rows: count, LatencyMs: latencyMs}
	result.Emit(ctx, c.vu, c.backend)
	return result.ToMap()
}

// Insert inserts a single document and emits metrics.
func (c *K6Client) Insert(table string, doc map[string]interface{}) map[string]interface{} {
	return c.InsertBatch(table, []map[string]interface{}{doc})
}

// Close closes the underlying driver.
func (c *K6Client) Close() {
	c.driver.Close()
}

// CLILoader wraps a Driver for CLI bulk loading.
type CLILoader struct {
	driver     Driver
	name       string
	fileType   string // "sql" or "json"
	connString string
	factory    DriverFactory
}

// NewCLILoader creates a CLI loader that wraps a driver.
func NewCLILoader(name, fileType, connString string, factory DriverFactory) *CLILoader {
	return &CLILoader{
		name:       name,
		fileType:   fileType,
		connString: connString,
		factory:    factory,
	}
}

func (l *CLILoader) ensureDriver() error {
	if l.driver != nil {
		return nil
	}
	driver, err := l.factory(l.connString)
	if err != nil {
		return err
	}
	l.driver = driver
	return nil
}

func (l *CLILoader) Name() string { return l.name }

func (l *CLILoader) RunPre(ctx context.Context, dir string, schema *Schema) error {
	return l.execFile(ctx, filepath.Join(dir, "pre."+l.fileType))
}

func (l *CLILoader) RunPost(ctx context.Context, dir string, schema *Schema) error {
	return l.execFile(ctx, filepath.Join(dir, "post."+l.fileType))
}

func (l *CLILoader) execFile(ctx context.Context, path string) error {
	if err := l.ensureDriver(); err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Optional file
		}
		return err
	}

	return l.driver.Exec(ctx, string(data))
}

func (l *CLILoader) Load(ctx context.Context, schema *Schema, csvPath string, batchSize int, workers int) (int, error) {
	if err := l.ensureDriver(); err != nil {
		return 0, err
	}

	file, err := os.Open(csvPath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	headers, err := reader.Read()
	if err != nil {
		return 0, err
	}

	// Map headers to column indices
	headerIdx := make(map[string]int)
	for i, h := range headers {
		headerIdx[h] = i
	}

	// Determine which columns to use from schema
	var cols []string
	for col := range schema.Columns {
		if _, ok := headerIdx[col]; ok {
			cols = append(cols, col)
		}
	}

	// Get target (table/index/collection)
	target := schema.Table
	if target == "" {
		target = schema.Index
	}
	if target == "" {
		target = schema.Collection
	}

	total := 0
	batch := make([][]any, 0, batchSize)

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return total, err
		}

		row := make([]any, len(cols))
		for i, col := range cols {
			row[i] = record[headerIdx[col]]
		}
		batch = append(batch, row)

		if len(batch) >= batchSize {
			n, err := l.driver.Insert(ctx, target, cols, batch)
			if err != nil {
				return total, err
			}
			total += n
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		n, err := l.driver.Insert(ctx, target, cols, batch)
		if err != nil {
			return total, err
		}
		total += n
	}

	return total, nil
}

func (l *CLILoader) Drop(ctx context.Context, schema *Schema) error {
	// Drop is handled by pre.sql/pre.json now
	return nil
}

func (l *CLILoader) Close() error {
	if l.driver != nil {
		return l.driver.Close()
	}
	return nil
}
