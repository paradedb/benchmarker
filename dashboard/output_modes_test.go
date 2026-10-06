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
		in                                         string
		wantLive, wantJSON, wantHTML, wantQueryCSV bool
		wantErr                                    bool
	}{
		{"", true, false, false, false, false},
		{"live", true, false, false, false, false},
		{"json", false, true, false, false, false},
		{"html", false, false, true, false, false},
		{"query_csv", false, false, false, true, false},
		{"live,html", true, false, true, false, false},
		{"live,json,html,query_csv", true, true, true, true, false},
		{" html , query_csv , json ", false, true, true, true, false},
		{"csv", false, false, false, false, true},
		{"foo", false, false, false, false, true},
		{"live,foo", false, false, false, false, true},
	}
	for _, c := range cases {
		live, gotJSON, gotHTML, gotQueryCSV, err := parseOutputModes(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseOutputModes(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if live != c.wantLive || gotJSON != c.wantJSON || gotHTML != c.wantHTML || gotQueryCSV != c.wantQueryCSV {
			t.Errorf("parseOutputModes(%q) = (live=%v, json=%v, html=%v, query_csv=%v); want (live=%v, json=%v, html=%v, query_csv=%v)",
				c.in, live, gotJSON, gotHTML, gotQueryCSV, c.wantLive, c.wantJSON, c.wantHTML, c.wantQueryCSV)
		}
	}
}

func TestNewRejectsUnknownMode(t *testing.T) {
	if _, err := New(output.Params{ConfigArgument: "live,bogus"}); err == nil {
		t.Fatalf("expected error for unknown mode, got nil")
	}
}

func TestDashboardExportPrefix(t *testing.T) {
	t.Setenv("DASHBOARD_EXPORT_PREFIX", "")
	if got, err := dashboardExportPrefix(); err != nil || got != "dashboard" {
		t.Fatalf("default export prefix = %q, %v", got, err)
	}

	t.Setenv("DASHBOARD_EXPORT_PREFIX", "search_2-terms.v1")
	if got, err := dashboardExportPrefix(); err != nil || got != "search_2-terms.v1" {
		t.Fatalf("custom export prefix = %q, %v", got, err)
	}

	t.Setenv("DASHBOARD_EXPORT_PREFIX", "../results")
	if _, err := dashboardExportPrefix(); err == nil {
		t.Fatal("expected error for unsafe export prefix")
	}
}

func TestNewCreatesDashboardExportDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "exports")
	t.Setenv("DASHBOARD_EXPORT_DIR", dir)

	if _, err := New(output.Params{ConfigArgument: "json"}); err != nil {
		t.Fatalf("New: %v", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("export directory = %#v, %v; want directory", info, err)
	}
}

func TestNewValidatesQueryCSVSeriesLimit(t *testing.T) {
	t.Setenv("DASHBOARD_QUERY_CSV_MAX_SERIES", "0")
	if _, err := New(output.Params{ConfigArgument: "query_csv"}); err == nil {
		t.Fatal("expected query_csv to reject a non-positive series limit")
	}

	// The setting is irrelevant when query_csv is disabled.
	if _, err := New(output.Params{ConfigArgument: "json"}); err != nil {
		t.Fatalf("json output rejected an unused query_csv setting: %v", err)
	}
}

// TestStartStopWritesRequestedExportFiles drives the full Start/Stop lifecycle
// through New() so we exercise the same code path k6 does, minus the http
// server (live mode disabled so no port binding).
func TestStartStopWritesRequestedExportFiles(t *testing.T) {
	cases := []struct {
		arg          string
		wantJSON     bool
		wantHTML     bool
		wantQueryCSV bool
	}{
		{"json", true, false, false},
		{"html", false, true, false},
		{"query_csv", false, false, true},
		{"json,html,query_csv", true, true, true},
	}
	for _, c := range cases {
		t.Run(c.arg, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("DASHBOARD_EXPORT_DIR", dir)
			t.Setenv("DASHBOARD_EXPORT_PREFIX", "bench")

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

			var hasJSON, hasHTML, hasQueryCSV bool
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("readdir: %v", err)
			}
			for _, e := range entries {
				name := e.Name()
				if !strings.HasPrefix(name, "bench_") {
					continue
				}
				if strings.HasSuffix(name, "_queries.csv") {
					hasQueryCSV = true
				} else if strings.HasSuffix(name, ".json") {
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
			if hasQueryCSV != c.wantQueryCSV {
				t.Errorf("query CSV file present=%v, want=%v", hasQueryCSV, c.wantQueryCSV)
			}
		})
	}
}
