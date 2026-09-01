package backends

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/pgvector/pgvector-go"
)

func TestNormalizeSchemaType(t *testing.T) {
	cases := map[string]string{
		"vector(768)":  "vector",
		"VECTOR":       "vector",
		"varchar(255)": "varchar",
		" text ":       "text",
	}
	for input, want := range cases {
		if got := normalizeSchemaType(input); got != want {
			t.Errorf("normalizeSchemaType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestConvertValueParsesVector(t *testing.T) {
	got, err := convertValue("[0.5, -1.25, 2]", "vector(3)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	vec, ok := got.(pgvector.Vector)
	if !ok {
		t.Fatalf("expected pgvector.Vector, got %T", got)
	}
	want := []float32{0.5, -1.25, 2}
	slice := vec.Slice()
	if len(slice) != len(want) {
		t.Fatalf("expected %d elements, got %d", len(want), len(slice))
	}
	for i := range want {
		if slice[i] != want[i] {
			t.Fatalf("element %d: expected %v, got %v", i, want[i], slice[i])
		}
	}
}

func TestConvertValueRejectsInvalidVector(t *testing.T) {
	if _, err := convertValue("not-a-vector", "vector"); err == nil {
		t.Fatal("expected error for invalid vector")
	}
}

func TestConvertValueRejectsWrongVectorDimension(t *testing.T) {
	_, err := convertValue("[1,2]", "vector(3)")
	if err == nil {
		t.Fatal("expected error for wrong vector dimension")
	}
	if !strings.Contains(err.Error(), "expects 3 values, got 2") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSchemaRejectsMalformedVectorType(t *testing.T) {
	loader := NewCLILoader("postgres", "sql", "stub://default", func(string) (Driver, error) {
		t.Fatal("factory should not be called during validation")
		return nil, nil
	})

	err := loader.ValidateSchema(&Schema{
		Columns: map[string]string{"emb": "vector(nope)"},
	})
	if err == nil {
		t.Fatal("expected malformed vector type error")
	}
	if !strings.Contains(err.Error(), `column "emb"`) {
		t.Fatalf("expected column context in error, got %v", err)
	}
}

func TestCLILoaderRejectsVectorSchemaForUnsupportedBackend(t *testing.T) {
	loader := NewCLILoader("clickhouse", "sql", "stub://default", func(string) (Driver, error) {
		t.Fatal("factory should not be called for unsupported vector schema")
		return nil, nil
	})

	_, err := loader.Load(context.Background(), &Schema{
		Table:   "documents",
		Columns: map[string]string{"emb": "vector(3)"},
	}, filepath.Join(t.TempDir(), "missing.parquet"), 100, 1)
	if err == nil {
		t.Fatal("expected unsupported vector schema error")
	}
	if !strings.Contains(err.Error(), `backend "clickhouse" does not support vector columns: emb`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

type capturingDriver struct {
	mu   sync.Mutex
	cols []string
	rows [][]any
}

func (d *capturingDriver) Close() error                                       { return nil }
func (d *capturingDriver) Exec(context.Context, string) error                 { return nil }
func (d *capturingDriver) Query(context.Context, string, ...any) (int, error) { return 0, nil }
func (d *capturingDriver) CaptureConfig(context.Context, string)              {}
func (d *capturingDriver) Insert(_ context.Context, _ string, cols []string, rows [][]any) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cols = cols
	d.rows = append(d.rows, rows...)
	return len(rows), nil
}
func (d *capturingDriver) Update(_ context.Context, _ string, _ []string, _ []string, rows [][]any) (int, error) {
	return len(rows), nil
}

type parquetTestRow struct {
	ID    int64     `parquet:"id"`
	Title string    `parquet:"title"`
	Emb   []float32 `parquet:"emb"`
}

type parquetScalarRow struct {
	ID     int32    `parquet:"id"`
	Hits   int64    `parquet:"hits"`
	Active bool     `parquet:"active"`
	Title  string   `parquet:"title"`
	Tags   []string `parquet:"tags,list"`
}

func writeParquetFixture(t *testing.T, rows []parquetTestRow) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.parquet")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create parquet file: %v", err)
	}
	writer := parquet.NewGenericWriter[parquetTestRow](file)
	if _, err := writer.Write(rows); err != nil {
		t.Fatalf("write parquet rows: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close parquet writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close parquet file: %v", err)
	}
	return path
}

func writeParquetScalarFixture(t *testing.T, rows []parquetScalarRow) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.parquet")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create parquet file: %v", err)
	}
	writer := parquet.NewGenericWriter[parquetScalarRow](file)
	if _, err := writer.Write(rows); err != nil {
		t.Fatalf("write parquet rows: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close parquet writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close parquet file: %v", err)
	}
	return path
}

func writeStandardListParquetFixture(t *testing.T, rows []map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.parquet")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create parquet file: %v", err)
	}
	schema := parquet.NewSchema("cohere", parquet.Group{
		"_id": parquet.String(),
		"emb": parquet.List(parquet.Leaf(parquet.FloatType)),
	})
	writer := parquet.NewGenericWriter[map[string]any](file, schema)
	if _, err := writer.Write(rows); err != nil {
		t.Fatalf("write parquet rows: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close parquet writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close parquet file: %v", err)
	}
	return path
}

func TestCLILoaderLoadsParquetWithVectors(t *testing.T) {
	path := writeParquetFixture(t, []parquetTestRow{
		{ID: 1, Title: "first", Emb: []float32{0.1, 0.2, 0.3}},
		{ID: 2, Title: "second", Emb: []float32{-1, 0, 1}},
	})

	driver := &capturingDriver{}
	loader := NewCLILoader("postgres", "sql", "stub://default", func(string) (Driver, error) {
		return driver, nil
	})

	count, err := loader.Load(context.Background(), &Schema{
		Table: "documents",
		Columns: map[string]string{
			"id":    "bigint",
			"title": "text",
			"emb":   "vector(3)",
		},
	}, path, 100, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows loaded, got %d", count)
	}

	byCol := make(map[string]any, len(driver.cols))
	for i, col := range driver.cols {
		byCol[col] = driver.rows[0][i]
	}
	if got, ok := byCol["id"].(int64); !ok || got != 1 {
		t.Fatalf("expected id 1, got %#v", byCol["id"])
	}
	if got, ok := byCol["title"].(string); !ok || got != "first" {
		t.Fatalf("expected title \"first\", got %#v", byCol["title"])
	}
	vec, ok := byCol["emb"].(pgvector.Vector)
	if !ok {
		t.Fatalf("expected pgvector.Vector, got %T", byCol["emb"])
	}
	if slice := vec.Slice(); len(slice) != 3 || slice[0] != 0.1 {
		t.Fatalf("unexpected vector contents: %v", vec.Slice())
	}
}

func TestCLILoaderLoadsParquetScalarsForNonPostgresBackend(t *testing.T) {
	path := writeParquetScalarFixture(t, []parquetScalarRow{
		{ID: 7, Hits: 42, Active: true, Title: "scalar", Tags: []string{"a", "b"}},
	})

	driver := &capturingDriver{}
	loader := NewCLILoader("clickhouse", "sql", "stub://default", func(string) (Driver, error) {
		return driver, nil
	})

	count, err := loader.Load(context.Background(), &Schema{
		Table: "documents",
		Columns: map[string]string{
			"id":     "integer",
			"hits":   "bigint",
			"active": "boolean",
			"title":  "text",
			"tags":   "text[]",
		},
	}, path, 100, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row loaded, got %d", count)
	}

	byCol := make(map[string]any, len(driver.cols))
	for i, col := range driver.cols {
		byCol[col] = driver.rows[0][i]
	}
	if got, ok := byCol["id"].(int32); !ok || got != 7 {
		t.Fatalf("expected int32 id 7, got %#v", byCol["id"])
	}
	if got, ok := byCol["hits"].(int64); !ok || got != 42 {
		t.Fatalf("expected int64 hits 42, got %#v", byCol["hits"])
	}
	if got, ok := byCol["active"].(bool); !ok || !got {
		t.Fatalf("expected active true, got %#v", byCol["active"])
	}
	if got, ok := byCol["title"].(string); !ok || got != "scalar" {
		t.Fatalf("expected title scalar, got %#v", byCol["title"])
	}
	if got := byCol["tags"]; !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("expected tags [a b], got %#v", got)
	}
}

func TestCLILoaderLoadsStandardListParquetVector(t *testing.T) {
	path := writeStandardListParquetFixture(t, []map[string]any{
		{"_id": "doc-1", "emb": []float32{0.1, 0.2, 0.3}},
	})

	driver := &capturingDriver{}
	loader := NewCLILoader("postgres", "sql", "stub://default", func(string) (Driver, error) {
		return driver, nil
	})

	count, err := loader.Load(context.Background(), &Schema{
		Table: "documents",
		Columns: map[string]string{
			"_id": "text",
			"emb": "vector(3)",
		},
	}, path, 100, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row loaded, got %d", count)
	}

	byCol := make(map[string]any, len(driver.cols))
	for i, col := range driver.cols {
		byCol[col] = driver.rows[0][i]
	}
	vec, ok := byCol["emb"].(pgvector.Vector)
	if !ok {
		t.Fatalf("expected pgvector.Vector, got %T", byCol["emb"])
	}
	if slice := vec.Slice(); len(slice) != 3 || slice[2] != 0.3 {
		t.Fatalf("unexpected vector contents: %v", vec.Slice())
	}
}

func TestConvertParquetValueRejectsWrongVectorDimension(t *testing.T) {
	_, err := convertParquetValue([]float32{1, 2}, "vector(3)")
	if err == nil {
		t.Fatal("expected vector dimension error")
	}
	if !strings.Contains(err.Error(), "expects 3 values, got 2") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCLILoaderLoadsShardedParquetDirectory(t *testing.T) {
	dir := t.TempDir()
	shards := [][]parquetTestRow{
		{{ID: 1, Title: "a", Emb: []float32{1, 0}}, {ID: 2, Title: "b", Emb: []float32{0, 1}}},
		{{ID: 3, Title: "c", Emb: []float32{1, 1}}},
	}
	for i, rows := range shards {
		data, err := os.ReadFile(writeParquetFixture(t, rows))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		shard := filepath.Join(dir, fmt.Sprintf("%04d.parquet", i))
		if err := os.WriteFile(shard, data, 0644); err != nil {
			t.Fatalf("write shard %s: %v", shard, err)
		}
	}

	driver := &capturingDriver{}
	loader := NewCLILoader("postgres", "sql", "stub://default", func(string) (Driver, error) {
		return driver, nil
	})

	count, err := loader.Load(context.Background(), &Schema{
		Table: "cohere_wiki",
		Columns: map[string]string{
			"id":    "bigint",
			"title": "text",
			"emb":   "vector(2)",
		},
	}, dir, 100, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 rows loaded across shards, got %d", count)
	}
}

func TestOpenRowSourceRejectsUnknownExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.jsonl")
	if err := os.WriteFile(path, []byte(`{"id":1}`), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := OpenRowSource(path, &Schema{Columns: map[string]string{"id": "text"}}); err == nil {
		t.Fatal("expected error for unsupported data file extension")
	}
}

func TestParquetSourceRejectsMissingSchemaColumn(t *testing.T) {
	path := writeParquetFixture(t, []parquetTestRow{{ID: 1, Title: "only", Emb: []float32{1}}})

	_, err := OpenRowSource(path, &Schema{
		Columns: map[string]string{"id": "bigint", "missing": "text"},
	})
	if err == nil {
		t.Fatal("expected error for schema column missing from parquet file")
	}
}
