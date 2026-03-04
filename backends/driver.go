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

// SplitSQLStatements splits SQL script into statements while respecting strings and comments.
func SplitSQLStatements(script string) []string {
	script = strings.TrimSpace(script)
	if script == "" {
		return nil
	}

	var statements []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	inLineComment := false
	inBlockComment := false
	inDollarQuote := false
	dollarTag := ""

	flush := func() {
		stmt := strings.TrimSpace(current.String())
		if stmt != "" {
			statements = append(statements, stmt)
		}
		current.Reset()
	}

	for i := 0; i < len(script); i++ {
		ch := script[i]
		var next byte
		if i+1 < len(script) {
			next = script[i+1]
		}

		if inLineComment {
			current.WriteByte(ch)
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			current.WriteByte(ch)
			if ch == '*' && next == '/' {
				current.WriteByte(next)
				i++
				inBlockComment = false
			}
			continue
		}
		if inDollarQuote {
			if strings.HasPrefix(script[i:], dollarTag) {
				current.WriteString(dollarTag)
				i += len(dollarTag) - 1
				inDollarQuote = false
				continue
			}
			current.WriteByte(ch)
			continue
		}
		if inSingle {
			current.WriteByte(ch)
			if ch == '\'' {
				// Escaped single quote ('')
				if next == '\'' {
					current.WriteByte(next)
					i++
				} else {
					inSingle = false
				}
			}
			continue
		}
		if inDouble {
			current.WriteByte(ch)
			if ch == '"' {
				inDouble = false
			}
			continue
		}

		if ch == '-' && next == '-' {
			current.WriteString("--")
			i++
			inLineComment = true
			continue
		}
		if ch == '/' && next == '*' {
			current.WriteString("/*")
			i++
			inBlockComment = true
			continue
		}
		if ch == '\'' {
			current.WriteByte(ch)
			inSingle = true
			continue
		}
		if ch == '"' {
			current.WriteByte(ch)
			inDouble = true
			continue
		}
		if ch == '$' {
			end := i + 1
			for end < len(script) {
				c := script[end]
				isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
				if c == '_' || isAlphaNum {
					end++
					continue
				}
				break
			}
			if end < len(script) && script[end] == '$' {
				dollarTag = script[i : end+1]
				current.WriteString(dollarTag)
				i = end
				inDollarQuote = true
				continue
			}
		}
		if ch == ';' {
			flush()
			continue
		}

		current.WriteByte(ch)
	}

	flush()
	return statements
}

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

// Search executes a query and emits metrics.
func (c *K6Client) Search(query string, args ...any) map[string]interface{} {
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
	metrics.CaptureQueryPattern(c.vu, queryPattern)
	metrics.CaptureScenarioInfo(c.vu)

	start := time.Now()
	hits, err := c.driver.Query(ctx, query, args...)
	latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

	if err != nil {
		fmt.Printf("[%s] search error: %v\n", c.backend, err)
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
	c.emitInitMetrics()

	fmt.Printf("[%s] InsertBatch called: table=%s docs=%d\n", c.backend, table, len(docs))

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

// Close closes the underlying driver.
func (c *K6Client) Close() {
	c.driver.Close()
}

// convertValue converts a raw CSV string value to the appropriate Go type based on schema type.
func convertValue(rawValue, schemaType string) any {
	// Strip null bytes (invalid in PostgreSQL text)
	rawValue = strings.ReplaceAll(rawValue, "\x00", "")

	// Handle empty strings as NULL for most types
	if rawValue == "" {
		return nil
	}

	schemaType = strings.ToLower(schemaType)

	switch schemaType {
	case "bigint", "int8":
		v, err := strconv.ParseInt(rawValue, 10, 64)
		if err != nil {
			return nil
		}
		return v

	case "integer", "int", "int4":
		v, err := strconv.ParseInt(rawValue, 10, 32)
		if err != nil {
			return nil
		}
		return int32(v)

	case "boolean", "bool":
		return rawValue == "true" || rawValue == "t" || rawValue == "1"

	case "bigint[]", "int8[]":
		var arr []int64
		if err := json.Unmarshal([]byte(rawValue), &arr); err != nil {
			return []int64{} // Return empty array on parse error
		}
		return arr

	case "integer[]", "int[]", "int4[]":
		var arr []int32
		if err := json.Unmarshal([]byte(rawValue), &arr); err != nil {
			return []int32{}
		}
		return arr

	case "text[]", "varchar[]":
		var arr []string
		if err := json.Unmarshal([]byte(rawValue), &arr); err != nil {
			return []string{}
		}
		return arr

	case "timestamp", "timestamptz":
		// Try common timestamp formats
		formats := []string{
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05Z",
			"2006-01-02T15:04:05-07:00",
			"2006-01-02",
		}
		for _, format := range formats {
			if t, err := time.Parse(format, rawValue); err == nil {
				return t
			}
		}
		return nil // Unparseable timestamp

	case "jsonb", "json":
		// Parse JSON into a map for ES/OS compatibility
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(rawValue), &obj); err != nil {
			return rawValue // Fall back to string if not valid JSON
		}
		return obj

	default:
		// text, varchar, etc - return as-is
		return rawValue
	}
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

	// Determine which columns to use from schema
	var cols []string
	for col := range schema.Columns {
		if _, ok := headerIdx[col]; ok {
			cols = append(cols, col)
		}
	}
	if len(cols) == 0 {
		return 0, fmt.Errorf("no schema columns matched CSV headers")
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
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			setErr(err)
			break
		}

		row := make([]any, len(cols))
		for i, col := range cols {
			idx, ok := headerIdx[col]
			if !ok || idx >= len(record) {
				row[i] = nil
				continue
			}
			row[i] = convertValue(record[idx], schema.Columns[col])
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
			cfg := map[string]interface{}{
				"database":   "benchmark",
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
