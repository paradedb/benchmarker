package loader

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/paradedb/benchmarker/backends"
	"go.k6.io/k6/js/modules"
	"gopkg.in/yaml.v3"
)

// DocumentStream is an independent, forward-only reader. It never caches the
// dataset or wraps at EOF. Like its VU, it is used by one goroutine at a time.
type DocumentStream struct {
	vu     modules.VU
	path   string
	schema *backends.Schema
	source backends.RowSource
	eof    bool
	closed bool
}

// OpenDocumentStream opens a CSV file, Parquet file, or directory of Parquet
// shards using a single-table schema. Both paths are relative to the k6 script.
// Data files are opened on the first nextBatch call, so unused init-context
// instances hold no file descriptors. Each call creates an independent reader.
func (l *Loader) OpenDocumentStream(path string, options map[string]interface{}) (*DocumentStream, error) {
	schemaPath, ok := options["schema"].(string)
	if !ok || schemaPath == "" {
		return nil, fmt.Errorf("openDocumentStream: schema must be a non-empty path")
	}
	if path == "" {
		return nil, fmt.Errorf("openDocumentStream: data path is required")
	}
	if l.vu != nil && l.vu.InitEnv() != nil {
		path = l.vu.InitEnv().GetAbsFilePath(path)
		schemaPath = l.vu.InitEnv().GetAbsFilePath(schemaPath)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("openDocumentStream: %w", err)
	}
	ext := strings.ToLower(filepath.Ext(path))
	if !info.IsDir() && ext != ".csv" && ext != ".parquet" {
		return nil, fmt.Errorf("openDocumentStream: expected .csv, .parquet, or a Parquet directory: %q", path)
	}
	// #nosec G304 -- Schema paths are selected by the locally executed script.
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("openDocumentStream: schema %q: %w", schemaPath, err)
	}
	var schema backends.Schema
	if err := yaml.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("openDocumentStream: schema %q: %w", schemaPath, err)
	}
	if err := schema.Validate(); err != nil {
		return nil, fmt.Errorf("openDocumentStream: schema %q: %w", schemaPath, err)
	}
	if len(schema.Tables) != 0 || len(schema.Columns) == 0 {
		return nil, fmt.Errorf("openDocumentStream: schema must describe a single table with columns")
	}
	return &DocumentStream{vu: l.vu, path: path, schema: &schema}, nil
}

// NextBatch returns up to n documents, filling across shard boundaries. EOF is
// an empty array, including repeated calls. Read failures discard the incomplete
// batch and close the stream; callers must not treat failures as EOF.
func (s *DocumentStream) NextBatch(n int) ([]map[string]interface{}, error) {
	if n <= 0 {
		return nil, fmt.Errorf("nextBatch: batch size must be greater than zero")
	}
	if s.closed {
		return nil, fmt.Errorf("nextBatch: stream is closed")
	}
	docs := make([]map[string]interface{}, 0)
	if s.eof {
		return docs, nil
	}
	ctx := context.Background()
	if s.vu != nil {
		if s.vu.State() == nil {
			return nil, fmt.Errorf("nextBatch: read documents in a VU function, not init context")
		}
		ctx = s.vu.Context()
	}
	if err := ctx.Err(); err != nil {
		_ = s.Close()
		return nil, err
	}
	if s.source == nil {
		source, err := backends.OpenRowSource(s.path, s.schema)
		if err != nil {
			s.closed = true
			return nil, fmt.Errorf("nextBatch: %q: %w", s.path, err)
		}
		s.source = source
	}
	columns := s.source.Columns()
	for len(docs) < n {
		if err := ctx.Err(); err != nil {
			s.closed = true
			_ = s.closeSource()
			return nil, err
		}
		row, err := s.source.Next()
		if err == io.EOF {
			s.eof = true
			if err := s.closeSource(); err != nil {
				s.closed = true
				return nil, fmt.Errorf("nextBatch: close %q: %w", s.path, err)
			}
			return docs, nil
		}
		if err != nil {
			s.closed = true
			_ = s.closeSource()
			return nil, fmt.Errorf("nextBatch: %q: %w", s.path, err)
		}
		doc := make(map[string]interface{}, len(columns))
		for i, column := range columns {
			doc[column] = row[i]
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

func (s *DocumentStream) closeSource() error {
	if s.source == nil {
		return nil
	}
	err := s.source.Close()
	s.source = nil
	return err
}

// Close releases resources. Calling it more than once is safe.
func (s *DocumentStream) Close() error {
	s.closed = true
	return s.closeSource()
}
