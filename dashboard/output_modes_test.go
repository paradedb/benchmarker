package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.k6.io/k6/output"
)

func TestParseOutputModes(t *testing.T) {
	cases := []struct {
		in                           string
		wantLive, wantJSON, wantHTML bool
		wantErr                      bool
	}{
		{"", true, false, false, false},
		{"live", true, false, false, false},
		{"json", false, true, false, false},
		{"html", false, false, true, false},
		{"live,html", true, false, true, false},
		{"live,json,html", true, true, true, false},
		{" html , json ", false, true, true, false},
		{"foo", false, false, false, true},
		{"live,foo", false, false, false, true},
	}
	for _, c := range cases {
		live, gotJSON, gotHTML, err := parseOutputModes(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseOutputModes(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if live != c.wantLive || gotJSON != c.wantJSON || gotHTML != c.wantHTML {
			t.Errorf("parseOutputModes(%q) = (live=%v, json=%v, html=%v); want (live=%v, json=%v, html=%v)",
				c.in, live, gotJSON, gotHTML, c.wantLive, c.wantJSON, c.wantHTML)
		}
	}
}

func TestNewRejectsUnknownMode(t *testing.T) {
	if _, err := New(output.Params{ConfigArgument: "live,bogus"}); err == nil {
		t.Fatalf("expected error for unknown mode, got nil")
	}
}

// TestStartStopWritesRequestedExportFiles drives the full Start/Stop lifecycle
// through New() so we exercise the same code path k6 does, minus the http
// server (live mode disabled so no port binding).
func TestStartStopWritesRequestedExportFiles(t *testing.T) {
	cases := []struct {
		arg      string
		wantJSON bool
		wantHTML bool
	}{
		{"json", true, false},
		{"html", false, true},
		{"json,html", true, true},
	}
	for _, c := range cases {
		t.Run(c.arg, func(t *testing.T) {
			dir := t.TempDir()
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatalf("getwd: %v", err)
			}
			if err := os.Chdir(dir); err != nil {
				t.Fatalf("chdir: %v", err)
			}
			t.Cleanup(func() { _ = os.Chdir(cwd) })

			out, err := New(output.Params{ConfigArgument: c.arg})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := out.Start(); err != nil {
				t.Fatalf("Start: %v", err)
			}
			if err := out.Stop(); err != nil {
				t.Fatalf("Stop: %v", err)
			}

			var hasJSON, hasHTML bool
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("readdir: %v", err)
			}
			for _, e := range entries {
				name := e.Name()
				if !strings.HasPrefix(name, "dashboard_") {
					continue
				}
				if strings.HasSuffix(name, ".json") {
					hasJSON = true
				}
				if strings.HasSuffix(name, ".html") {
					hasHTML = true
					content, err := os.ReadFile(filepath.Join(dir, name))
					if err != nil {
						t.Fatalf("read html: %v", err)
					}
					if !strings.Contains(string(content), "__DASHBOARD_EMBEDDED_DATA") {
						t.Errorf("HTML export missing embedded data marker")
					}
				}
			}
			if hasJSON != c.wantJSON {
				t.Errorf("JSON file present=%v, want=%v", hasJSON, c.wantJSON)
			}
			if hasHTML != c.wantHTML {
				t.Errorf("HTML file present=%v, want=%v", hasHTML, c.wantHTML)
			}
		})
	}
}
