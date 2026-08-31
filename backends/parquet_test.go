package backends

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

func TestCLILoaderLoadsParquetWithVectors(t *testing.T) {
	path := writeParquetFixture(t, []parquetTestRow{
		{ID: 1, Title: "first", Emb: []float32{0.1, 0.2, 0.3}},
		{ID: 2, Title: "second", Emb: []float32{-1, 0, 1}},
	})

	driver := &capturingDriver{}
	loader := NewCLILoader("test", "sql", "stub://default", func(string) (Driver, error) {
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
	loader := NewCLILoader("test", "sql", "stub://default", func(string) (Driver, error) {
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
	if _, err := OpenRowSource("data.jsonl", &Schema{Columns: map[string]string{"id": "text"}}); err == nil {
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
