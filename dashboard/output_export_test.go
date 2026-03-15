package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportStandaloneEscapesEmbeddedScriptTerminators(t *testing.T) {
	dir := t.TempDir()
	jsonFile := filepath.Join(dir, "dashboard.json")
	htmlFile := filepath.Join(dir, "dashboard.html")

	data := `{"runs":{},"containers":{},"elapsed":0}`
	if err := os.WriteFile(jsonFile, []byte(data), 0644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	notes := `</script><script>alert("xss")</script>`
	if err := ExportStandalone(jsonFile, htmlFile, notes); err != nil {
		t.Fatalf("export standalone: %v", err)
	}

	html, err := os.ReadFile(htmlFile)
	if err != nil {
		t.Fatalf("read html: %v", err)
	}

	content := string(html)
	if strings.Contains(content, notes) {
		t.Fatalf("expected script terminators to be escaped in exported HTML")
	}
	if !strings.Contains(content, `\u003c/script\u003e\u003cscript\u003ealert(\"xss\")\u003c/script\u003e`) {
		t.Fatalf("expected escaped notes payload in exported HTML, got %q", content)
	}
}
