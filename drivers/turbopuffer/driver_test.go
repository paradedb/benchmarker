package turbopuffer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/paradedb/benchmarker/drivers"
)

func testDriver(t *testing.T, handler http.HandlerFunc) *Driver {
	t.Helper()
	t.Setenv("TURBOPUFFER_API_KEY", "test-secret")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing authorization")
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	driver, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	d := driver.(*Driver)
	d.client.Transport = server.Client().Transport
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestConnection(t *testing.T) {
	t.Setenv("TURBOPUFFER_API_KEY", "test-secret")
	t.Setenv("TURBOPUFFER_URL", "")
	t.Setenv("TURBOPUFFER_REGION", "aws-us-east-1")
	driver, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	d := driver.(*Driver)
	defer d.Close()
	if d.address != "https://aws-us-east-1.turbopuffer.com" {
		t.Fatal(d.address)
	}
	cfg, ok := drivers.GetConfig("turbopuffer")
	if !ok || !cfg.OptionalConnection || cfg.FileType != "json" {
		t.Fatalf("bad registration: %+v", cfg)
	}
	for _, url := range []string{"http://host", "https://user:secret@host", "https://host/path", "https://host?api_key=secret", "https://host?distance_metric=bad", "https://host#fragment"} {
		if _, err := New(url); err == nil {
			t.Errorf("accepted %s", url)
		}
	}
	t.Setenv("TURBOPUFFER_URL", "https://gcp-us-central1.turbopuffer.com?distance_metric=euclidean_squared")
	driver, err = New("")
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	if driver.(*Driver).distanceMetric != "euclidean_squared" {
		t.Fatal("URL option ignored")
	}
	t.Setenv("TURBOPUFFER_API_KEY", "")
	if _, err := New(""); err == nil {
		t.Fatal("missing key accepted")
	}
}

func TestQuery(t *testing.T) {
	cases := []struct {
		name, response string
		hits           int
	}{
		{"rows", `{"rows":[{"id":1},{"id":2}]}`, 2},
		{"empty", `{"rows":[]}`, 0},
		{"count", `{"aggregations":{"count":17}}`, 17},
		{"groups", `{"aggregation_groups":[{},{}]}`, 2},
		{"multi", `{"results":[{"rows":[{}]},{"rows":[{},{}]}]}`, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/v2/namespaces/docs/query" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["top_k"] != float64(10) {
					t.Error(body)
				}
				fmt.Fprint(w, tc.response)
			})
			for _, object := range []bool{true, false} {
				var n int
				var err error
				if object {
					n, err = d.Query(context.Background(), "docs", map[string]any{"top_k": 10})
				} else {
					n, err = d.Query(context.Background(), `{"top_k":10}`, "docs")
				}
				if err != nil || n != tc.hits {
					t.Fatalf("hits=%d err=%v", n, err)
				}
			}
		})
	}
}

func TestUpsertAndValidation(t *testing.T) {
	var requests atomic.Int32
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v2/namespaces/docs" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		var body struct {
			Rows   []map[string]any `json:"upsert_rows"`
			Metric string           `json:"distance_metric"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Rows) != 1 || body.Rows[0]["id"] != "a" || body.Metric != "cosine_distance" {
			t.Errorf("bad body %+v", body)
		}
		if _, ok := body.Rows[0]["_id"]; ok {
			t.Error("_id leaked")
		}
		if vector := decodedVector(t, body.Rows[0]["vector"]); len(vector) != 2 || vector[0] != 0.25 || vector[1] != 0.5 {
			t.Error("missing vector")
		}
		fmt.Fprint(w, `{"rows_affected":1}`)
	})
	rows := [][]any{{"a", []float32{0.25, 0.5}}}
	n, err := d.Insert(context.Background(), "docs", []string{"_id", "vector"}, rows)
	if n != 1 || err != nil {
		t.Fatalf("%d %v", n, err)
	}
	n, err = d.Update(context.Background(), "docs", []string{"id"}, []string{"id", "vector"}, rows)
	if n != 1 || err != nil {
		t.Fatalf("%d %v", n, err)
	}
	for _, tc := range []struct {
		cols []string
		rows [][]any
	}{
		{[]string{"text"}, [][]any{{"a"}}},
		{[]string{"id", "text"}, [][]any{{"a"}}},
		{[]string{"id"}, [][]any{{nil}}},
		{[]string{"id", "id"}, [][]any{{"a", "b"}}},
	} {
		if _, err := d.Insert(context.Background(), "docs", tc.cols, tc.rows); err == nil {
			t.Error("invalid row accepted")
		}
	}
	if _, err := d.Update(context.Background(), "docs", []string{"id", "other"}, nil, rows); err == nil {
		t.Error("composite ID accepted")
	}
	if n, err := d.Insert(context.Background(), "docs", nil, nil); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if requests.Load() != 2 {
		t.Fatal("invalid writes reached server")
	}
}

func TestLoaderParallelSetupAndDrop(t *testing.T) {
	var deletes, writes atomic.Int32
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/namespaces/docs" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Method == "DELETE" {
			deletes.Add(1)
			w.WriteHeader(404)
			return
		}
		writes.Add(1)
		var body struct {
			Rows   []map[string]any `json:"upsert_rows"`
			Schema map[string]any   `json:"schema"`
			Metric string           `json:"distance_metric"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Schema["vector"] == nil || body.Metric != "euclidean_squared" {
			t.Errorf("worker lost setup: %+v", body)
		}
		fmt.Fprintf(w, `{"rows_affected":%d}`, len(body.Rows))
	})
	dir := t.TempDir()
	pre := `{"namespace":"docs","reset":true,"distance_metric":"euclidean_squared","schema":{"vector":{"type":"[2]f32","ann":true}}}`
	if err := os.WriteFile(filepath.Join(dir, "pre.json"), []byte(pre), 0600); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(data, []byte("id,vector\n1,\"[1,2]\"\n2,\"[2,3]\"\n3,\"[3,4]\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loader := drivers.NewCLILoader("turbopuffer", "json", "", func(string) (drivers.Driver, error) { return d, nil })
	schema := &drivers.Schema{Table: "docs", Columns: map[string]string{"id": "int", "vector": "vector(2)"}}
	if err := loader.RunPre(context.Background(), dir, schema); err != nil {
		t.Fatal(err)
	}
	n, err := loader.Load(context.Background(), schema, data, 1, 3)
	if err != nil || n != 3 {
		t.Fatalf("load %d: %v", n, err)
	}
	if deletes.Load() != 1 || writes.Load() != 3 {
		t.Fatalf("deletes=%d writes=%d", deletes.Load(), writes.Load())
	}
	if err := loader.Drop(context.Background(), schema); err != nil {
		t.Fatal(err)
	}
	if deletes.Load() != 2 {
		t.Fatal("drop did not delete namespace")
	}
}

func TestOperationsErrorsAndCancellation(t *testing.T) {
	var calls atomic.Int32
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/v1/namespaces/docs/hint_cache_warm", "/v1/namespaces/docs/metadata":
			if r.Method != "GET" {
				t.Error(r.Method)
			}
			if r.ContentLength != 0 {
				t.Error("GET operation sent an unexpected body")
			}
			fmt.Fprint(w, `{}`)
		default:
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":"rate limited test-secret"}`)
		}
	})
	if err := d.Exec(context.Background(), `[{"namespace":"docs","method":"GET","endpoint":"hint_cache_warm"},{"namespace":"docs","method":"GET","endpoint":"metadata"}]`); err != nil {
		t.Fatal(err)
	}
	_, err := d.Query(context.Background(), "docs", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("bad error %v", err)
	}
	if calls.Load() != 3 {
		t.Fatal("unexpected retries")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Query(ctx, "docs", map[string]any{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, config := range []string{"null", `{"schema":[]}`, `{"namespace":"../bad","reset":true}`, `{"distance_metric":"invalid","reset":true}`} {
		if err := d.Exec(context.Background(), config); err == nil {
			t.Errorf("accepted %s", config)
		}
	}
	if calls.Load() != 3 {
		t.Fatal("invalid requests reached server")
	}
}

func TestInvalidResponses(t *testing.T) {
	for _, response := range []string{`oops`, `{}`} {
		t.Run(response, func(t *testing.T) {
			d := testDriver(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, response) })
			if _, err := d.Insert(context.Background(), "docs", []string{"id"}, [][]any{{1}}); err == nil {
				t.Fatal("accepted invalid write response")
			}
		})
	}
}

func TestNumericNamespaceAndIDs(t *testing.T) {
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/namespaces/123/query" {
			t.Errorf("wrong namespace path: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"rows":[]}`)
	})
	if _, err := d.Query(context.Background(), "123", map[string]any{"top_k": 1}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []any{nil, "", strings.Repeat("x", 65), -1, 1.5, 9007199254740992.0, true, []int{1}} {
		if err := validateID(id); err == nil {
			t.Errorf("accepted invalid ID %#v", id)
		}
	}
	for _, id := range []any{"uuid-string", 0, int64(9007199254740993), uint64(18446744073709551615), 42.0, json.Number("18446744073709551615")} {
		if err := validateID(id); err != nil {
			t.Errorf("rejected ID %#v: %v", id, err)
		}
	}
}

func TestMultipleNamespaceSetup(t *testing.T) {
	recorder := &featureRecorder{}
	d := testDriver(t, recorder.serve(t))
	pre := `[
  {"namespace":"one","reset":true,"schema":{"title":{"type":"string","full_text_search":true}}},
  {"namespace":"two","distance_metric":"euclidean_squared","schema":{"vector":{"type":"[2]f32","ann":true}}}
 ]`
	if err := d.Exec(context.Background(), pre); err != nil {
		t.Fatal(err)
	}
	worker, err := d.NewLoadWorker()
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	if _, err := worker.Insert(context.Background(), "one", []string{"id", "title"}, [][]any{{1, "text"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Insert(context.Background(), "two", []string{"id", "vector"}, [][]any{{1, []float32{1, 0}}}); err != nil {
		t.Fatal(err)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.deletes) != 1 || recorder.writes[0].Schema["title"] == nil || recorder.writes[1].Schema["vector"] == nil || recorder.writes[1].Metric != "euclidean_squared" {
		t.Fatalf("wrong namespace setup: %+v", recorder.writes)
	}
}

func TestWaitForIndex(t *testing.T) {
	var calls atomic.Int32
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/namespaces/docs/metadata" {
			t.Errorf("wrong metadata request: %s %s", r.Method, r.URL)
		}
		status := "updating"
		if calls.Add(1) > 1 {
			status = "up-to-date"
		}
		fmt.Fprintf(w, `{"index":{"status":%q}}`, status)
	})
	if err := d.Exec(context.Background(), `[{"namespace":"docs","wait_for_index":true,"timeout":"3s"}]`); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
	calls.Store(0)
	err := d.Exec(context.Background(), `[{"namespace":"docs","wait_for_index":true,"timeout":"10ms"}]`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait did not time out: %v", err)
	}
}

func TestTLSAndRedirects(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	if _, err := d.Query(context.Background(), "docs", map[string]any{}); err == nil {
		t.Fatal("followed redirect")
	}
	if targetCalls.Load() != 0 {
		t.Fatal("credentials sent to redirect target")
	}
	untrusted, err := New(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer untrusted.Close()
	if _, err := untrusted.Query(context.Background(), "docs", map[string]any{}); err == nil {
		t.Fatal("accepted untrusted certificate")
	}
}

func TestVectorEncodingPreservesFloat32(t *testing.T) {
	values := []float32{-1.234567, 0, 1e-10, 3.1415927}
	encoded, err := encodeVector(values)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodedVector(t, encoded)
	for i, value := range values {
		if decoded[i] != value {
			t.Fatalf("value %d changed: %v -> %v", i, value, decoded[i])
		}
	}
	if _, err := encodeVector([]float32{float32(math.Inf(1))}); err == nil {
		t.Fatal("accepted infinity")
	}
	if _, err := encodeVector([]float32{float32(math.NaN())}); err == nil {
		t.Fatal("accepted NaN")
	}
}
