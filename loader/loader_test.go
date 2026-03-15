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
