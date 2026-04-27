// Package backends provides the driver interface and shared infrastructure for search backends.
package backends

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/paradedb/benchmarks/metrics"
	"go.k6.io/k6/js/modules"
)

// Schema defines the dataset schema from schema.yaml
type Schema struct {
	Table   string            `yaml:"table"`
	Columns map[string]string `yaml:"columns"`
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

	names := make([]string, 0, len(backendConfigs))
	for name := range backendConfigs {
		names = append(names, name)
	}
	sort.Strings(names)

	loaders := make([]*CLILoader, 0, len(names))
	for _, name := range names {
		cfg := backendConfigs[name]
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
	sort.Strings(names)
	return names
}

// Driver is the minimal interface each backend must implement.
type Driver interface {
	Close() error
	Exec(ctx context.Context, statements string) error
	Query(ctx context.Context, query string, args ...any) (hitCount int, err error)
	Insert(ctx context.Context, table string, cols []string, rows [][]any) (int, error)
	Update(ctx context.Context, table string, keyCols []string, cols []string, rows [][]any) (int, error)
	CaptureConfig(ctx context.Context, backendName string)
}

// DriverFactory creates a driver from a connection string.
type DriverFactory func(connString string) (Driver, error)

// K6Client wraps a Driver with k6 metrics emission.
type K6Client struct {
	driver      Driver
	vu          modules.VU
	backend     string
	timeout     time.Duration
	initialized bool // Track if backend_init has been emitted
}

// NewK6Client creates a k6 client that wraps a driver.
func NewK6Client(vu modules.VU, driver Driver, backend string) *K6Client {
	metrics.RegisterMetrics(vu)
	return &K6Client{driver: driver, vu: vu, backend: backend, timeout: 0}
}

// SetTimeout sets the query timeout duration.
// Use 0 to disable timeout (default).
func (c *K6Client) SetTimeout(seconds int) {
	c.timeout = time.Duration(seconds) * time.Second
}

// emitInitMetrics emits initialization metrics on first call to signal dashboard.
func (c *K6Client) emitInitMetrics() {
	if c.initialized {
		return
	}
	c.initialized = true
	metrics.EmitBackendInit(c.vu, c.backend)
	metrics.EmitScenarioStarted(c.vu, c.backend)
}

// Query executes a query and emits metrics.
func (c *K6Client) Query(query string, args ...any) map[string]interface{} {
	c.emitInitMetrics()

	ctx := context.Background()
	var cancel context.CancelFunc
	if c.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	// Capture query pattern - for ES/OS style queries (index, queryObj), serialize the query object
	queryPattern := strings.TrimSpace(query)
	if len(args) > 0 {
		if queryMap, ok := args[0].(map[string]interface{}); ok {
			if jsonBytes, err := json.Marshal(queryMap); err == nil {
				queryPattern = string(jsonBytes)
			}
		}
	}
	metrics.CaptureQueryPattern(c.vu, c.backend, queryPattern)
	metrics.CaptureScenarioInfo(c.vu)

	start := time.Now()
	hits, err := c.driver.Query(ctx, query, args...)
	latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

	if err != nil {
		fmt.Printf("[%s] query error: %v\n", c.backend, err)
		return map[string]interface{}{
			"hits":      0,
			"latencyMs": latencyMs,
			"error":     err.Error(),
		}
	}

	result := &metrics.QueryResult{Hits: int64(hits), LatencyMs: latencyMs}
	result.Emit(ctx, c.vu, c.backend)
	return result.ToMap()
}

// InsertBatch inserts documents and emits metrics.
func (c *K6Client) InsertBatch(table string, docs []map[string]interface{}) map[string]interface{} {
	c.emitInitMetrics()

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
		fmt.Printf("[%s] insert error: %v\n", c.backend, err)
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

// UpdateBatch updates documents and emits metrics.
// The first column is used as the key for matching existing rows.
func (c *K6Client) UpdateBatch(table string, docs []map[string]interface{}) map[string]interface{} {
	c.emitInitMetrics()

	if len(docs) == 0 {
		return map[string]interface{}{"rows": 0, "latencyMs": 0.0}
	}

	// Extract columns from first doc; "id" is the key column
	var keyCols []string
	var valCols []string
	for col := range docs[0] {
		if col == "id" || col == "_id" {
			keyCols = append(keyCols, col)
		} else {
			valCols = append(valCols, col)
		}
	}
	allCols := append(keyCols, valCols...)

	// Convert docs to rows
	rows := make([][]any, len(docs))
	for i, doc := range docs {
		row := make([]any, len(allCols))
		for j, col := range allCols {
			row[j] = doc[col]
		}
		rows[i] = row
	}

	ctx := context.Background()
	start := time.Now()
	count, err := c.driver.Update(ctx, table, keyCols, allCols, rows)
	latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

	if err != nil {
		fmt.Printf("[%s] update error: %v\n", c.backend, err)
		return map[string]interface{}{
			"rows":      0,
			"latencyMs": latencyMs,
			"error":     err.Error(),
		}
	}

	result := &metrics.UpdateResult{Rows: count, LatencyMs: latencyMs}
	result.Emit(ctx, c.vu, c.backend)
	return result.ToMap()
}

// Update updates a single document and emits metrics.
func (c *K6Client) Update(table string, doc map[string]interface{}) map[string]interface{} {
	return c.UpdateBatch(table, []map[string]interface{}{doc})
}

// Close closes the underlying driver.
func (c *K6Client) Close() {
	c.driver.Close()
}

// convertValue converts a raw CSV string value to the appropriate Go type based on schema type.
func convertValue(rawValue, schemaType string) (any, error) {
	// Strip null bytes (invalid in PostgreSQL text)
	rawValue = strings.ReplaceAll(rawValue, "\x00", "")

	schemaType = strings.ToLower(schemaType)
	if rawValue == "" {
		switch schemaType {
		case "text", "varchar", "char", "character varying", "string":
			return "", nil
		default:
			return nil, nil
		}
	}

	switch schemaType {
	case "bigint", "int8":
		v, err := strconv.ParseInt(rawValue, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid bigint %q: %w", rawValue, err)
		}
		return v, nil

	case "integer", "int", "int4":
		v, err := strconv.ParseInt(rawValue, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid integer %q: %w", rawValue, err)
		}
		return int32(v), nil

	case "boolean", "bool":
		switch strings.ToLower(rawValue) {
		case "true", "t", "1":
			return true, nil
		case "false", "f", "0":
			return false, nil
		default:
			return nil, fmt.Errorf("invalid boolean %q", rawValue)
		}

	case "bigint[]", "int8[]":
		var arr []int64
		if err := json.Unmarshal([]byte(rawValue), &arr); err != nil {
			return nil, fmt.Errorf("invalid bigint array %q: %w", rawValue, err)
		}
		return arr, nil

	case "integer[]", "int[]", "int4[]":
		var arr []int32
		if err := json.Unmarshal([]byte(rawValue), &arr); err != nil {
			return nil, fmt.Errorf("invalid integer array %q: %w", rawValue, err)
		}
		return arr, nil

	case "text[]", "varchar[]":
		var arr []string
		if err := json.Unmarshal([]byte(rawValue), &arr); err != nil {
			return nil, fmt.Errorf("invalid text array %q: %w", rawValue, err)
		}
		return arr, nil

	case "timestamp", "timestamptz":
		formats := []string{
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05Z",
			"2006-01-02T15:04:05-07:00",
			"2006-01-02",
		}
		for _, format := range formats {
			if t, err := time.Parse(format, rawValue); err == nil {
				return t, nil
			}
		}
		return nil, fmt.Errorf("invalid timestamp %q", rawValue)

	case "jsonb", "json":
		var obj interface{}
		if err := json.Unmarshal([]byte(rawValue), &obj); err != nil {
			return nil, fmt.Errorf("invalid json %q: %w", rawValue, err)
		}
		return obj, nil

	default:
		// text, varchar, etc - return as-is
		return rawValue, nil
	}
}

func schemaColumnsInOrder(schema *Schema, headers []string) ([]string, error) {
	if schema == nil || len(schema.Columns) == 0 {
		return nil, fmt.Errorf("schema has no columns")
	}

	seen := make(map[string]struct{}, len(headers))
	cols := make([]string, 0, len(headers))
	for _, header := range headers {
		if _, ok := schema.Columns[header]; ok {
			cols = append(cols, header)
			seen[header] = struct{}{}
		}
	}

	var missing []string
	for col := range schema.Columns {
		if _, ok := seen[col]; !ok {
			missing = append(missing, col)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("schema columns missing from CSV: %s", strings.Join(missing, ", "))
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("no schema columns matched CSV headers")
	}

	return cols, nil
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
	if workers < 1 {
		workers = 1
	}
	if batchSize < 1 {
		batchSize = 1
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

	cols, err := schemaColumnsInOrder(schema, headers)
	if err != nil {
		return 0, err
	}

	target := schema.Table
	if target == "" {
		target = "documents"
	}

	// Reuse loader driver for worker 0 and create additional drivers as needed.
	drivers := make([]Driver, 0, workers)
	drivers = append(drivers, l.driver)
	for i := 1; i < workers; i++ {
		d, err := l.factory(l.connString)
		if err != nil {
			for j := 1; j < len(drivers); j++ {
				drivers[j].Close()
			}
			return 0, err
		}
		drivers = append(drivers, d)
	}
	defer func() {
		for i := 1; i < len(drivers); i++ {
			drivers[i].Close()
		}
	}()

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	batchCh := make(chan [][]any, workers*2)
	var wg sync.WaitGroup
	var totalMu sync.Mutex
	total := 0
	var firstErr error
	var errOnce sync.Once
	setErr := func(e error) {
		if e == nil {
			return
		}
		errOnce.Do(func() {
			firstErr = e
			cancel()
		})
	}

	for _, d := range drivers {
		driver := d
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rows := range batchCh {
				n, err := driver.Insert(workerCtx, target, cols, rows)
				if err != nil {
					setErr(err)
					return
				}
				totalMu.Lock()
				total += n
				totalMu.Unlock()
			}
		}()
	}

	sendBatch := func(rows [][]any) bool {
		if len(rows) == 0 {
			return true
		}
		rowsCopy := make([][]any, len(rows))
		copy(rowsCopy, rows)
		select {
		case <-workerCtx.Done():
			return false
		case batchCh <- rowsCopy:
			return true
		}
	}

	batch := make([][]any, 0, batchSize)
	rowNum := 1 // header row
rowLoop:
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			setErr(fmt.Errorf("failed to read CSV row %d from %q: %w", rowNum+1, csvPath, err))
			break
		}
		rowNum++

		row := make([]any, len(cols))
		for i, col := range cols {
			idx, ok := headerIdx[col]
			if !ok || idx >= len(record) {
				row[i] = nil
				continue
			}
			value, err := convertValue(record[idx], schema.Columns[col])
			if err != nil {
				setErr(fmt.Errorf("row %d column %q: %w", rowNum, col, err))
				break rowLoop
			}
			row[i] = value
		}
		batch = append(batch, row)

		if len(batch) >= batchSize {
			if !sendBatch(batch) {
				break
			}
			batch = make([][]any, 0, batchSize)
		}
	}

	if firstErr == nil && len(batch) > 0 {
		sendBatch(batch)
	}
	close(batchCh)
	wg.Wait()

	if firstErr != nil {
		return total, firstErr
	}
	if workerCtx.Err() != nil && workerCtx.Err() != context.Canceled {
		return total, workerCtx.Err()
	}

	return total, nil
}

func safeSQLIdentifier(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty identifier")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		isLower := c >= 'a' && c <= 'z'
		isUpper := c >= 'A' && c <= 'Z'
		isDigit := c >= '0' && c <= '9'
		isUnderscore := c == '_'
		if i == 0 {
			if !(isLower || isUpper || isUnderscore) {
				return "", fmt.Errorf("invalid identifier: %q", name)
			}
		} else if !(isLower || isUpper || isDigit || isUnderscore) {
			return "", fmt.Errorf("invalid identifier: %q", name)
		}
	}
	return name, nil
}

func (l *CLILoader) Drop(ctx context.Context, schema *Schema) error {
	if err := l.ensureDriver(); err != nil {
		return err
	}

	target := "documents"
	if schema != nil && schema.Table != "" {
		target = schema.Table
	}

	switch l.fileType {
	case "sql":
		table, err := safeSQLIdentifier(target)
		if err != nil {
			return err
		}
		return l.driver.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", table))
	case "json":
		switch l.name {
		case "elasticsearch", "opensearch":
			ops := []map[string]interface{}{
				{
					"method": "DELETE",
					"index":  target,
				},
			}
			data, err := json.Marshal(ops)
			if err != nil {
				return err
			}
			return l.driver.Exec(ctx, string(data))
		case "mongodb":
			database := "benchmark"
			if provider, ok := l.driver.(interface{ DatabaseName() string }); ok {
				if name := provider.DatabaseName(); name != "" {
					database = name
				}
			}
			cfg := map[string]interface{}{
				"database":   database,
				"collection": target,
				"drop":       true,
			}
			data, err := json.Marshal(cfg)
			if err != nil {
				return err
			}
			return l.driver.Exec(ctx, string(data))
		default:
			return fmt.Errorf("drop not supported for backend %s", l.name)
		}
	default:
		return fmt.Errorf("unsupported backend file type %s", l.fileType)
	}
}

func (l *CLILoader) Close() error {
	if l.driver != nil {
		return l.driver.Close()
	}
	return nil
}
