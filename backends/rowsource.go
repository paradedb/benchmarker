package backends

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// RowSource streams schema-converted rows from a dataset file.
type RowSource interface {
	// Columns returns the schema columns present in the file, in file order.
	Columns() []string
	// Next returns the values for the next row, aligned with Columns().
	// It returns io.EOF when the source is exhausted.
	Next() ([]any, error)
	Close() error
}

// OpenRowSource opens a dataset data file or a directory of parquet shards,
// dispatching on file extension.
func OpenRowSource(path string, schema *Schema) (RowSource, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return openParquetDirSource(path, schema)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".parquet":
		return openParquetSource(path, schema)
	case ".csv":
		return openCSVSource(path, schema)
	default:
		return nil, fmt.Errorf("unsupported data file %q: expected .csv or .parquet", path)
	}
}

type csvSource struct {
	file    *os.File
	path    string
	reader  *csv.Reader
	schema  *Schema
	cols    []string
	indexes []int
	rowNum  int
}

func openCSVSource(path string, schema *Schema) (RowSource, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	reader := csv.NewReader(file)
	headers, err := reader.Read()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to read CSV headers from %q: %w", path, err)
	}

	cols, err := schemaColumnsInOrder(schema, headers)
	if err != nil {
		file.Close()
		return nil, err
	}

	headerIdx := make(map[string]int, len(headers))
	for i, h := range headers {
		headerIdx[h] = i
	}
	indexes := make([]int, len(cols))
	for i, col := range cols {
		indexes[i] = headerIdx[col]
	}

	return &csvSource{
		file:    file,
		path:    path,
		reader:  reader,
		schema:  schema,
		cols:    cols,
		indexes: indexes,
		rowNum:  1, // header row
	}, nil
}

func (s *csvSource) Columns() []string { return s.cols }

func (s *csvSource) Next() ([]any, error) {
	record, err := s.reader.Read()
	if err == io.EOF {
		return nil, io.EOF
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV row %d from %q: %w", s.rowNum+1, s.path, err)
	}
	s.rowNum++

	row := make([]any, len(s.cols))
	for i, col := range s.cols {
		idx := s.indexes[i]
		if idx >= len(record) {
			row[i] = nil
			continue
		}
		value, err := convertValue(record[idx], s.schema.Columns[col])
		if err != nil {
			return nil, fmt.Errorf("row %d column %q: %w", s.rowNum, col, err)
		}
		row[i] = value
	}
	return row, nil
}

func (s *csvSource) Close() error { return s.file.Close() }
