package backends

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/pgvector/pgvector-go"
)

const parquetReadBatch = 256

type parquetSource struct {
	file       *os.File
	path       string
	fileSchema *parquet.Schema
	schema     *Schema
	cols       []string
	groups     []parquet.RowGroup
	group      int
	rows       parquet.Rows
	buf        []parquet.Row
	pos        int
	n          int
	rowNum     int64
}

func openParquetSource(path string, schema *Schema) (RowSource, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}

	pf, err := parquet.OpenFile(file, info.Size())
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to open parquet file %q: %w", path, err)
	}

	fields := pf.Schema().Fields()
	headers := make([]string, len(fields))
	for i, f := range fields {
		headers[i] = f.Name()
	}

	cols, err := schemaColumnsInOrder(schema, headers)
	if err != nil {
		file.Close()
		return nil, err
	}

	return &parquetSource{
		file:       file,
		path:       path,
		fileSchema: pf.Schema(),
		schema:     schema,
		cols:       cols,
		groups:     pf.RowGroups(),
		buf:        make([]parquet.Row, parquetReadBatch),
	}, nil
}

func (s *parquetSource) Columns() []string { return s.cols }

func (s *parquetSource) Next() ([]any, error) {
	for s.pos >= s.n {
		if err := s.fill(); err != nil {
			return nil, err
		}
	}
	row := s.buf[s.pos]
	s.pos++
	s.rowNum++
	return s.convert(row)
}

func (s *parquetSource) fill() error {
	for {
		if s.rows == nil {
			if s.group >= len(s.groups) {
				return io.EOF
			}
			s.rows = s.groups[s.group].Rows()
			s.group++
		}

		n, err := s.rows.ReadRows(s.buf)
		s.pos, s.n = 0, n
		if err == io.EOF {
			closeErr := s.rows.Close()
			s.rows = nil
			if closeErr != nil {
				return closeErr
			}
			if n == 0 {
				continue
			}
			return nil
		}
		if err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
	}
}

func (s *parquetSource) convert(row parquet.Row) ([]any, error) {
	record, err := s.reconstruct(row)
	if err != nil {
		return nil, fmt.Errorf("parquet row %d in %q: %w", s.rowNum, s.path, err)
	}

	out := make([]any, len(s.cols))
	for i, col := range s.cols {
		value, err := convertParquetValue(record[col], s.schema.Columns[col])
		if err != nil {
			return nil, fmt.Errorf("parquet row %d column %q in %q: %w", s.rowNum, col, s.path, err)
		}
		out[i] = value
	}
	return out, nil
}

// reconstruct decodes a parquet row into a map keyed by column name.
// Schema.Reconstruct panics on schema mismatch, so recover into an error.
func (s *parquetSource) reconstruct(row parquet.Row) (record map[string]any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("failed to decode parquet row: %v", r)
		}
	}()
	record = make(map[string]any, len(s.cols))
	err = s.fileSchema.Reconstruct(&record, row)
	return record, err
}

// convertParquetValue maps a decoded parquet value to the Go type expected for
// the schema column. Scalars already carry usable Go types; vectors and any
// string-encoded values go through the schema-driven conversions.
func convertParquetValue(raw any, schemaType string) (any, error) {
	if raw == nil {
		return nil, nil
	}
	normalizedType := normalizeSchemaType(schemaType)
	if normalizedType == "vector" {
		return toVector(raw, schemaType)
	}
	if str, ok := raw.(string); ok {
		return convertValue(str, schemaType)
	}

	switch normalizedType {
	case "text", "varchar", "char", "character varying", "string", "uuid":
		return toText(raw)

	case "bigint", "int8":
		return toInt64(raw)

	case "integer", "int", "int4":
		v, err := toInt64(raw)
		if err != nil {
			return nil, err
		}
		if v < -2147483648 || v > 2147483647 {
			return nil, fmt.Errorf("integer value %d overflows int32", v)
		}
		return int32(v), nil

	case "boolean", "bool":
		v, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("invalid boolean value of type %T", raw)
		}
		return v, nil

	case "bigint[]", "int8[]":
		return toInt64Slice(raw)

	case "integer[]", "int[]", "int4[]":
		values, err := toInt64Slice(raw)
		if err != nil {
			return nil, err
		}
		out := make([]int32, len(values))
		for i, v := range values {
			if v < -2147483648 || v > 2147483647 {
				return nil, fmt.Errorf("integer array element %d overflows int32", v)
			}
			out[i] = int32(v)
		}
		return out, nil

	case "text[]", "varchar[]":
		return toStringSlice(raw)

	case "timestamp", "timestamptz":
		switch v := raw.(type) {
		case time.Time:
			return v, nil
		case []byte:
			return convertValue(string(v), schemaType)
		default:
			return nil, fmt.Errorf("invalid timestamp value of type %T", raw)
		}

	case "jsonb", "json":
		switch v := raw.(type) {
		case []byte:
			var obj any
			if err := json.Unmarshal(v, &obj); err != nil {
				return nil, fmt.Errorf("invalid json %q: %w", string(v), err)
			}
			return obj, nil
		default:
			return raw, nil
		}

	default:
		return raw, nil
	}
}

func toText(raw any) (string, error) {
	switch v := raw.(type) {
	case []byte:
		return string(v), nil
	default:
		return "", fmt.Errorf("invalid text value of type %T", raw)
	}
}

func toInt64(raw any) (int64, error) {
	switch v := raw.(type) {
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint:
		if uint64(v) > uint64(^uint64(0)>>1) {
			return 0, fmt.Errorf("integer value %d overflows int64", v)
		}
		return int64(v), nil
	case uint8:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case uint64:
		if v > uint64(^uint64(0)>>1) {
			return 0, fmt.Errorf("integer value %d overflows int64", v)
		}
		return int64(v), nil
	default:
		return 0, fmt.Errorf("invalid integer value of type %T", raw)
	}
}

func toInt64Slice(raw any) ([]int64, error) {
	switch v := raw.(type) {
	case []int:
		out := make([]int64, len(v))
		for i, e := range v {
			out[i] = int64(e)
		}
		return out, nil
	case []int32:
		out := make([]int64, len(v))
		for i, e := range v {
			out[i] = int64(e)
		}
		return out, nil
	case []int64:
		return v, nil
	case []any:
		out := make([]int64, len(v))
		for i, e := range v {
			value, err := toInt64(e)
			if err != nil {
				return nil, fmt.Errorf("array element %d: %w", i, err)
			}
			out[i] = value
		}
		return out, nil
	default:
		return nil, fmt.Errorf("invalid integer array value of type %T", raw)
	}
}

func toStringSlice(raw any) ([]string, error) {
	switch v := raw.(type) {
	case []string:
		return v, nil
	case [][]byte:
		out := make([]string, len(v))
		for i, e := range v {
			out[i] = string(e)
		}
		return out, nil
	case []any:
		out := make([]string, len(v))
		for i, e := range v {
			switch value := e.(type) {
			case string:
				out[i] = value
			case []byte:
				out[i] = string(value)
			default:
				return nil, fmt.Errorf("array element %d: invalid text value of type %T", i, e)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("invalid text array value of type %T", raw)
	}
}

func toVector(raw any, schemaType string) (any, error) {
	switch v := raw.(type) {
	case []float32:
		if err := validateVectorDimension(len(v), schemaType); err != nil {
			return nil, err
		}
		return pgvector.NewVector(v), nil
	case []float64:
		out := make([]float32, len(v))
		for i, f := range v {
			out[i] = float32(f)
		}
		if err := validateVectorDimension(len(out), schemaType); err != nil {
			return nil, err
		}
		return pgvector.NewVector(out), nil
	case []any:
		out := make([]float32, len(v))
		for i, e := range v {
			switch f := e.(type) {
			case float32:
				out[i] = f
			case float64:
				out[i] = float32(f)
			default:
				return nil, fmt.Errorf("invalid vector element of type %T", e)
			}
		}
		if err := validateVectorDimension(len(out), schemaType); err != nil {
			return nil, err
		}
		return pgvector.NewVector(out), nil
	case string:
		return convertValue(v, schemaType)
	default:
		return nil, fmt.Errorf("invalid vector value of type %T", raw)
	}
}

func (s *parquetSource) Close() error {
	if s.rows != nil {
		s.rows.Close()
		s.rows = nil
	}
	return s.file.Close()
}

type parquetDirSource struct {
	schema  *Schema
	paths   []string
	index   int
	current RowSource
	cols    []string
}

func openParquetDirSource(dir string, schema *Schema) (RowSource, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.ToLower(filepath.Ext(entry.Name())) == ".parquet" {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no .parquet files found in %q", dir)
	}

	first, err := openParquetSource(paths[0], schema)
	if err != nil {
		return nil, err
	}

	return &parquetDirSource{
		schema:  schema,
		paths:   paths,
		current: first,
		cols:    first.Columns(),
	}, nil
}

func (s *parquetDirSource) Columns() []string { return s.cols }

func (s *parquetDirSource) Next() ([]any, error) {
	for {
		row, err := s.current.Next()
		if err != io.EOF {
			return row, err
		}

		if err := s.current.Close(); err != nil {
			return nil, err
		}
		s.index++
		if s.index >= len(s.paths) {
			return nil, io.EOF
		}

		next, err := openParquetSource(s.paths[s.index], s.schema)
		if err != nil {
			return nil, err
		}
		if !slices.Equal(next.Columns(), s.cols) {
			next.Close()
			return nil, fmt.Errorf("parquet shard %q has different columns than %q", s.paths[s.index], s.paths[0])
		}
		s.current = next
	}
}

func (s *parquetDirSource) Close() error {
	if s.index < len(s.paths) {
		return s.current.Close()
	}
	return nil
}
