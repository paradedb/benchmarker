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
		t.Fatalf("expected empty text conversion to succeed, got %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty text value to stay empty, got %#v", got)
	}
}

func TestConvertValueKeepsEmptyIntegerAsNil(t *testing.T) {
	got, err := convertValue("", "integer")
	if err != nil {
		t.Fatalf("expected empty integer conversion to succeed, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected empty integer value to become nil, got %#v", got)
	}
}

func TestConvertValueRejectsInvalidJSONArray(t *testing.T) {
	_, err := convertValue("not-json", "text[]")
	if err == nil {
		t.Fatal("expected invalid array JSON to return an error")
	}
}

type recordingDriver struct {
	inserted int
}

func (d *recordingDriver) Close() error { return nil }

func (d *recordingDriver) Exec(context.Context, string) error { return nil }

func (d *recordingDriver) Query(context.Context, string, ...any) (int, error) { return 0, nil }

func (d *recordingDriver) Insert(_ context.Context, _ string, _ []string, rows [][]any) (int, error) {
	d.inserted += len(rows)
	return len(rows), nil
}

func (d *recordingDriver) CaptureConfig(context.Context, string) {}

func TestCLILoaderLoadFailsWhenSchemaColumnsAreMissingFromCSV(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(csvPath, []byte("id,title\n1,hello\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	driver := &recordingDriver{}
	loader := NewCLILoader("test", "sql", "stub://default", func(string) (Driver, error) {
		return driver, nil
	})

	_, err := loader.Load(context.Background(), &Schema{
		Table: "documents",
		Columns: map[string]string{
			"id":      "text",
			"title":   "text",
			"content": "text",
		},
	}, csvPath, 100, 1)
	if err == nil {
		t.Fatal("expected schema drift to return an error")
	}
	if !strings.Contains(err.Error(), "schema columns missing from CSV: content") {
		t.Fatalf("expected missing-column error, got %v", err)
	}
	if driver.inserted != 0 {
		t.Fatalf("expected no rows to be inserted, got %d", driver.inserted)
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
		Table: "documents",
		Columns: map[string]string{
			"numbers": "integer[]",
		},
	}, csvPath, 100, 1)
	if err == nil {
		t.Fatal("expected typed conversion error")
	}
	if !strings.Contains(err.Error(), `row 2 column "numbers"`) {
		t.Fatalf("expected row/column context in error, got %v", err)
	}
	if driver.inserted != 0 {
		t.Fatalf("expected no rows to be inserted, got %d", driver.inserted)
	}
}
