package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/paradedb/benchmarker/profiles"
)

func TestManifestColorsSurviveLiveExportAndReplay(t *testing.T) {
	for _, tc := range []struct{ backend, color string }{
		{"paradedb", ""}, {"postgres", ""}, {"clickhouse", ""},
		{"mongodb", ""}, {"elasticsearch", ""}, {"opensearch", ""},
		{"paradedb", "orange"}, {"my-profile", "#123456"}, {"unknown", ""},
	} {
		t.Run(tc.backend+tc.color, func(t *testing.T) {
			want := tc.color
			if want == "" && tc.backend != "unknown" {
				w, err := profiles.Builtin(tc.backend)
				if err != nil || w.Color == "" {
					t.Fatalf("missing builtin color: %+v, %v", w, err)
				}
				want = w.Color
			}
			o := &Output{data: &DashboardData{
				StartTime: time.Unix(0, 0),
				Runs: map[string]*RunMetrics{"search": {
					Backend: tc.backend, Color: tc.color, Measured: true,
					Queries: map[string]*QueryMetrics{},
				}},
				Containers: map[string]*ContainerMetrics{"db": {Backend: tc.backend, Color: tc.color}},
			}}
			check := func(t *testing.T, data map[string]interface{}) {
				t.Helper()
				for section, name := range map[string]string{"runs": "search", "containers": "db"} {
					entry := data[section].(map[string]interface{})[name].(map[string]interface{})
					if entry["color"] != want {
						t.Errorf("%s color = %v, want %q", section, entry["color"], want)
					}
				}
			}
			check(t, o.getSummary())
			check(t, o.getExportData())

			// Historical exports may omit color entirely or store an empty string.
			raw := map[string]interface{}{
				"runs":       map[string]interface{}{"search": map[string]interface{}{"backend": tc.backend}},
				"containers": map[string]interface{}{"db": map[string]interface{}{"backend": tc.backend, "color": tc.color}},
			}
			if tc.color != "" {
				raw["runs"].(map[string]interface{})["search"].(map[string]interface{})["color"] = tc.color
			}
			before, _ := json.Marshal(raw)
			check(t, aggregateExportData(raw, time.Second, 0))
			after, _ := json.Marshal(raw)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("replay mutated original recording")
			}

			file := filepath.Join(t.TempDir(), "dashboard.html")
			if err := emitStandaloneHTML(raw, file, time.Second, 0); err != nil {
				t.Fatal(err)
			}
			html, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			_, embedded, found := strings.Cut(string(html), "window.__DASHBOARD_EMBEDDED_DATA = ")
			if !found {
				t.Fatal("missing embedded data")
			}
			payload, _, _ := strings.Cut(embedded, ";</script>")
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &data); err != nil {
				t.Fatal(err)
			}
			check(t, data)
		})
	}
}
