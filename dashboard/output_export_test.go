package dashboard

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarshalExportJSONEndsWithNewline(t *testing.T) {
	data := map[string]interface{}{
		"startTime": 1785149352318,
		"runs":      map[string]interface{}{},
	}

	out, err := marshalExportJSON(data)
	if err != nil {
		t.Fatalf("marshal export json: %v", err)
	}

	if !bytes.HasSuffix(out, []byte("}\n")) {
		t.Fatalf("expected export to end with a trailing newline, got %q", tail(out))
	}
	if bytes.HasSuffix(out, []byte("\n\n")) {
		t.Fatalf("expected exactly one trailing newline, got %q", tail(out))
	}

	// The newline must not disturb the payload itself.
	var round map[string]interface{}
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("exported JSON no longer parses: %v", err)
	}
	if _, ok := round["startTime"]; !ok {
		t.Fatalf("expected startTime to survive the round trip, got %v", round)
	}
}

func tail(b []byte) string {
	if len(b) > 12 {
		b = b[len(b)-12:]
	}
	return string(b)
}

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
