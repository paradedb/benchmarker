// Package loader provides data loading functionality for k6 benchmarks.
package loader

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/paradedb/benchmarks/backends"
	"go.k6.io/k6/js/common"
	"go.k6.io/k6/js/modules"
)

// Loader handles loading data into search backends.
type Loader struct {
	vu modules.VU
}

func (l *Loader) throwConfigErrorf(format string, args ...interface{}) *DocumentReader {
	err := fmt.Errorf(format, args...)
	if l.vu != nil {
		if rt := l.vu.Runtime(); rt != nil {
			common.Throw(rt, err)
			return nil
		}
	}
	panic(err.Error())
}

// Global document cache - shared across all VUs
var (
	documentCache   = make(map[string]*DocumentReader)
	documentCacheMu sync.RWMutex
)

// DocumentReader provides access to documents from a CSV file.
type DocumentReader struct {
	documents []map[string]interface{}
	index     atomic.Int64
	size      int
}

// OpenDocuments loads documents from a CSV file.
// Defaults to "../data.csv" if no path provided.
// Paths are resolved relative to the k6 script location.
// The file is loaded once and cached - subsequent calls return the cached reader.
func (l *Loader) OpenDocuments(filePath string) *DocumentReader {
	if filePath == "" {
		filePath = "../data.csv"
	}

	// Resolve path relative to script location
	if l.vu != nil {
		if initEnv := l.vu.InitEnv(); initEnv != nil {
			filePath = initEnv.GetAbsFilePath(filePath)
		}
	}

	// Check cache first
	documentCacheMu.RLock()
	if reader, ok := documentCache[filePath]; ok {
		documentCacheMu.RUnlock()
		return reader
	}
	documentCacheMu.RUnlock()

	// Load file
	documentCacheMu.Lock()
	defer documentCacheMu.Unlock()

	// Double-check after acquiring write lock
	if reader, ok := documentCache[filePath]; ok {
		return reader
	}

	file, err := os.Open(filePath)
	if err != nil {
		return l.throwConfigErrorf("openDocuments: failed to open %q: %v", filePath, err)
	}
	defer file.Close()

	csvReader := csv.NewReader(file)

	// Read header
	headers, err := csvReader.Read()
	if err != nil {
		return l.throwConfigErrorf("openDocuments: failed to read CSV headers from %q: %v", filePath, err)
	}

	// Read all rows
	var docs []map[string]interface{}
	for {
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		doc := make(map[string]interface{}, len(headers))
		for i, header := range headers {
			if i < len(record) {
				doc[header] = record[i]
			}
		}
		docs = append(docs, doc)
	}

	reader := &DocumentReader{
		documents: docs,
		size:      len(docs),
	}
	documentCache[filePath] = reader
	return reader
}

// Next returns the next document, cycling through the dataset.
// Thread-safe via atomic counter.
func (r *DocumentReader) Next() map[string]interface{} {
	if r.size == 0 {
		return nil
	}
	idx := r.index.Add(1) - 1
	return r.documents[idx%int64(r.size)]
}

// NextBatch returns the next n documents, cycling through the dataset.
// Thread-safe via atomic counter.
func (r *DocumentReader) NextBatch(n int) []map[string]interface{} {
	if r.size == 0 || n <= 0 {
		return nil
	}

	startIdx := r.index.Add(int64(n)) - int64(n)
	batch := make([]map[string]interface{}, n)
	for i := 0; i < n; i++ {
		batch[i] = r.documents[(startIdx+int64(i))%int64(r.size)]
	}
	return batch
}

// NextBatchNewIds returns the next n documents with fresh UUIDs.
// Use this when re-inserting documents that already exist in the database.
// Thread-safe via atomic counter.
func (r *DocumentReader) NextBatchNewIds(n int) []map[string]interface{} {
	if r.size == 0 || n <= 0 {
		return nil
	}

	startIdx := r.index.Add(int64(n)) - int64(n)
	batch := make([]map[string]interface{}, n)
	for i := 0; i < n; i++ {
		orig := r.documents[(startIdx+int64(i))%int64(r.size)]
		// Copy the document and replace the id
		doc := make(map[string]interface{}, len(orig))
		for k, v := range orig {
			doc[k] = v
		}
		doc["id"] = uuid.New().String()
		batch[i] = doc
	}
	return batch
}

// Size returns the number of documents.
func (r *DocumentReader) Size() int {
	return r.size
}

// NewLoader creates a new data loader.
func NewLoader(vu modules.VU) *Loader {
	return &Loader{vu: vu}
}

// Helper to parse common config options
func parseConfig(config map[string]interface{}) (filePath, tableName, dataset string, batchSize int) {
	filePath, _ = config["file"].(string)
	tableName, _ = config["table"].(string)
	if tableName == "" {
		tableName = "documents"
	}
	dataset, _ = config["dataset"].(string)
	batchSize = 10000
	if bs, ok := config["batchSize"].(float64); ok {
		batchSize = int(bs)
	}
	return
}

func parseColumns(config map[string]interface{}, filePath string) ([]string, error) {
	if rawCols, ok := config["columns"].([]interface{}); ok && len(rawCols) > 0 {
		cols := make([]string, 0, len(rawCols))
		for _, c := range rawCols {
			s, ok := c.(string)
			if ok && s != "" {
				cols = append(cols, s)
			}
		}
		if len(cols) > 0 {
			return cols, nil
		}
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	headers, err := reader.Read()
	if err != nil {
		return nil, err
	}
	if len(headers) == 0 {
		return nil, fmt.Errorf("CSV has no headers")
	}
	return headers, nil
}

// readCSVDocuments reads documents from a CSV file.
func readCSVDocuments(filePath string) ([]map[string]interface{}, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	csvReader := csv.NewReader(file)

	// Read header
	headers, err := csvReader.Read()
	if err != nil {
		return nil, err
	}

	// Read all rows
	var docs []map[string]interface{}
	for {
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		doc := make(map[string]interface{}, len(headers))
		for i, header := range headers {
			if i < len(record) {
				doc[header] = record[i]
			}
		}
		docs = append(docs, doc)
	}

	return docs, nil
}

// readFile reads a file and returns its contents as a string.
func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// docsToRows converts documents to rows for Insert.
func docsToRows(docs []map[string]interface{}, columns []string) [][]any {
	rows := make([][]any, len(docs))
	for i, doc := range docs {
		row := make([]any, len(columns))
		for j, col := range columns {
			row[j] = doc[col]
		}
		rows[i] = row
	}
	return rows
}

// loadWithDriver is the generic loading function that works with any Driver.
func loadWithDriver(driver backends.Driver, tableName, filePath, dataset, backendDir string, columns []string, batchSize int) map[string]interface{} {
	ctx := context.Background()
	start := time.Now()

	// Run pre file if dataset provided
	if dataset != "" {
		prePath := filepath.Join(dataset, backendDir, "pre.sql")
		if content, err := readFile(prePath); err == nil {
			if err := driver.Exec(ctx, content); err != nil {
				return map[string]interface{}{"error": fmt.Sprintf("pre.sql failed: %v", err)}
			}
		}
	}

	// Read documents
	docs, err := readCSVDocuments(filePath)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("read file failed: %v", err)}
	}

	// Load in batches
	totalLoaded := 0
	for i := 0; i < len(docs); i += batchSize {
		end := i + batchSize
		if end > len(docs) {
			end = len(docs)
		}

		rows := docsToRows(docs[i:end], columns)
		n, err := driver.Insert(ctx, tableName, columns, rows)
		if err != nil {
			return map[string]interface{}{"error": fmt.Sprintf("insert failed: %v", err)}
		}
		totalLoaded += n
	}

	loadTime := time.Since(start)

	// Run post file if dataset provided
	var indexTime time.Duration
	if dataset != "" {
		indexStart := time.Now()
		postPath := filepath.Join(dataset, backendDir, "post.sql")
		if content, err := readFile(postPath); err == nil {
			if err := driver.Exec(ctx, content); err != nil {
				return map[string]interface{}{"error": fmt.Sprintf("post.sql failed: %v", err)}
			}
		}
		indexTime = time.Since(indexStart)
	}

	return map[string]interface{}{
		"loaded":      totalLoaded,
		"loadTimeMs":  loadTime.Milliseconds(),
		"indexTimeMs": indexTime.Milliseconds(),
		"totalTimeMs": time.Since(start).Milliseconds(),
	}
}

// loadWithDriverJSON is the generic loading function for JSON-config backends.
func loadWithDriverJSON(driver backends.Driver, tableName, filePath, dataset, backendDir string, columns []string, batchSize int) map[string]interface{} {
	ctx := context.Background()
	start := time.Now()

	// Run pre file if dataset provided
	if dataset != "" {
		prePath := filepath.Join(dataset, backendDir, "pre.json")
		if content, err := readFile(prePath); err == nil {
			if err := driver.Exec(ctx, content); err != nil {
				return map[string]interface{}{"error": fmt.Sprintf("pre.json failed: %v", err)}
			}
		}
	}

	// Read documents
	docs, err := readCSVDocuments(filePath)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("read file failed: %v", err)}
	}

	// Load in batches
	totalLoaded := 0
	for i := 0; i < len(docs); i += batchSize {
		end := i + batchSize
		if end > len(docs) {
			end = len(docs)
		}

		rows := docsToRows(docs[i:end], columns)
		n, err := driver.Insert(ctx, tableName, columns, rows)
		if err != nil {
			return map[string]interface{}{"error": fmt.Sprintf("insert failed: %v", err)}
		}
		totalLoaded += n
	}

	loadTime := time.Since(start)

	// Run post file if dataset provided
	var indexTime time.Duration
	if dataset != "" {
		indexStart := time.Now()
		postPath := filepath.Join(dataset, backendDir, "post.json")
		if content, err := readFile(postPath); err == nil {
			if err := driver.Exec(ctx, content); err != nil {
				return map[string]interface{}{"error": fmt.Sprintf("post.json failed: %v", err)}
			}
		}
		indexTime = time.Since(indexStart)
	}

	return map[string]interface{}{
		"loaded":      totalLoaded,
		"loadTimeMs":  loadTime.Milliseconds(),
		"indexTimeMs": indexTime.Milliseconds(),
		"totalTimeMs": time.Since(start).Milliseconds(),
	}
}

// Load loads CSV data into any registered backend.
// Config options:
//   - file: path to CSV file (required)
//   - table: table/index/collection name (default: "documents")
//   - dataset: path to dataset directory with backend-specific pre/post files
//   - batchSize: batch size for loading (default: 10000)
func (l *Loader) Load(backendName, connectionString string, config map[string]interface{}) map[string]interface{} {
	cfg, ok := backends.GetConfig(backendName)
	if !ok {
		return map[string]interface{}{"error": fmt.Sprintf("unknown backend: %s", backendName)}
	}

	filePath, tableName, dataset, batchSize := parseConfig(config)
	if filePath == "" {
		return map[string]interface{}{"error": "missing required config field: file"}
	}
	if batchSize <= 0 {
		return map[string]interface{}{"error": "batchSize must be greater than 0"}
	}

	columns, err := parseColumns(config, filePath)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("failed to determine columns: %v", err)}
	}

	driver, err := cfg.Factory(connectionString)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("connect failed: %v", err)}
	}
	defer driver.Close()

	if cfg.FileType == "json" {
		return loadWithDriverJSON(driver, tableName, filePath, dataset, backendName, columns, batchSize)
	}
	return loadWithDriver(driver, tableName, filePath, dataset, backendName, columns, batchSize)
}

// LoadParadeDB loads CSV data into ParadeDB.
func (l *Loader) LoadParadeDB(connectionString string, config map[string]interface{}) map[string]interface{} {
	return l.Load("paradedb", connectionString, config)
}

// LoadPostgresFTS loads CSV data into vanilla PostgreSQL with tsquery/tsvector.
func (l *Loader) LoadPostgresFTS(connectionString string, config map[string]interface{}) map[string]interface{} {
	return l.Load("postgresfts", connectionString, config)
}

// Load loads CSV data into PostgreSQL with  extension.
func (l *Loader) Load(connectionString string, config map[string]interface{}) map[string]interface{} {
	return l.Load("", connectionString, config)
}

// LoadElasticsearch loads CSV data into Elasticsearch.
func (l *Loader) LoadElasticsearch(config map[string]interface{}) map[string]interface{} {
	address, _ := config["address"].(string)
	if address == "" {
		cfg, _ := backends.GetConfig("elasticsearch")
		address = cfg.DefaultConn
	}
	return l.Load("elasticsearch", address, config)
}

// LoadClickHouse loads CSV data into ClickHouse.
func (l *Loader) LoadClickHouse(connectionString string, config map[string]interface{}) map[string]interface{} {
	return l.Load("clickhouse", connectionString, config)
}

// LoadMongoDB loads CSV data into MongoDB with Atlas Search index.
func (l *Loader) LoadMongoDB(connectionString string, config map[string]interface{}) map[string]interface{} {
	return l.Load("mongodb", connectionString, config)
}
