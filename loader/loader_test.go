package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCSVDocumentsReturnsParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.csv")
	data := "id,title\n1,ok\n2,\"unterminated\n"
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	_, err := readCSVDocuments(path)
	if err == nil {
		t.Fatal("expected parse error for malformed CSV")
	}
	if !strings.Contains(err.Error(), "row 3") {
		t.Fatalf("expected row number in error, got %v", err)
	}
}

func TestParseColumnsRejectsUnknownConfiguredColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(path, []byte("id,title\n1,ok\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	_, err := parseColumns(map[string]interface{}{
		"columns": []interface{}{"id", "missing"},
	}, path)
	if err == nil {
		t.Fatal("expected parseColumns to reject unknown configured columns")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing column name in error, got %v", err)
	}
}

func TestParseColumnsRejectsEmptyConfiguredColumn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(path, []byte("id,title\n1,ok\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	_, err := parseColumns(map[string]interface{}{
		"columns": []interface{}{"id", ""},
	}, path)
	if err == nil {
		t.Fatal("expected parseColumns to reject empty configured column names")
	}
	if !strings.Contains(err.Error(), "columns[1]") {
		t.Fatalf("expected column index in error, got %v", err)
	}
}
