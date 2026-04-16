package backends

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertValuePreservesEmptyText(t *testing.T) {
	got, err := convertValue("", "text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty text value to stay empty, got %#v", got)
	}
}

func TestConvertValueKeepsEmptyIntegerAsNil(t *testing.T) {
	got, err := convertValue("", "integer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected empty integer value to become nil, got %#v", got)
	}
}

func TestConvertValueRejectsInvalidInteger(t *testing.T) {
	_, err := convertValue("abc", "integer")
	if err == nil {
		t.Fatal("expected error for invalid integer")
	}
}

func TestConvertValueRejectsInvalidJSONArray(t *testing.T) {
	_, err := convertValue("not-json", "text[]")
	if err == nil {
		t.Fatal("expected error for invalid JSON array")
	}
}

func TestConvertValueRejectsInvalidBoolean(t *testing.T) {
	_, err := convertValue("maybe", "boolean")
	if err == nil {
		t.Fatal("expected error for invalid boolean")
	}
}

type recordingDriver struct {
	inserted int
}

func (d *recordingDriver) Close() error                                          { return nil }
func (d *recordingDriver) Exec(context.Context, string) error                    { return nil }
func (d *recordingDriver) Query(context.Context, string, ...any) (int, error)    { return 0, nil }
func (d *recordingDriver) CaptureConfig(context.Context, string)                 {}
func (d *recordingDriver) Insert(_ context.Context, _ string, _ []string, rows [][]any) (int, error) {
	d.inserted += len(rows)
	return len(rows), nil
}
func (d *recordingDriver) Update(_ context.Context, _ string, _ []string, _ []string, rows [][]any) (int, error) {
	return len(rows), nil
}

func TestSchemaColumnsInOrderRejectsMissingColumns(t *testing.T) {
	_, err := schemaColumnsInOrder(&Schema{
		Columns: map[string]string{"id": "text", "title": "text", "content": "text"},
	}, []string{"id", "title"})
	if err == nil {
		t.Fatal("expected error for missing schema column")
	}
	if !strings.Contains(err.Error(), "content") {
		t.Fatalf("expected missing column name in error, got %v", err)
	}
}

func TestCLILoaderLoadFailsOnInvalidTypedValue(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(csvPath, []byte("numbers\nnot-json\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	driver := &recordingDriver{}
	loader := NewCLILoader("test", "sql", "stub://default", func(string) (Driver, error) {
		return driver, nil
	})

	_, err := loader.Load(context.Background(), &Schema{
		Table:   "documents",
		Columns: map[string]string{"numbers": "integer[]"},
	}, csvPath, 100, 1)
	if err == nil {
		t.Fatal("expected error for invalid typed value")
	}
	if !strings.Contains(err.Error(), `row 2 column "numbers"`) {
		t.Fatalf("expected row/column context in error, got %v", err)
	}
	if driver.inserted != 0 {
		t.Fatalf("expected no rows inserted, got %d", driver.inserted)
	}
}
