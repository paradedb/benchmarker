//go:build integration

package turbopuffer

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/paradedb/benchmarker/backends"
	dataloader "github.com/paradedb/benchmarker/loader"
	"go.k6.io/k6/js/modulestest"
	"go.k6.io/k6/lib"
	k6metrics "go.k6.io/k6/metrics"
	"gopkg.in/yaml.v3"
)

var liveEnvFile = flag.String("turbopuffer-env-file", "", "Read TURBOPUFFER_API_KEY from a local .env for explicit live tests")
var liveRegion = flag.String("turbopuffer-region", "", "Region for explicit live tests")

func configureLiveTest(t *testing.T) {
	t.Helper()
	if *liveEnvFile != "" {
		data, err := os.ReadFile(*liveEnvFile)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			name, value, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "export "), "=")
			if ok && strings.TrimSpace(name) == "TURBOPUFFER_API_KEY" {
				t.Setenv("TURBOPUFFER_API_KEY", strings.Trim(strings.TrimSpace(value), "\"'"))
			}
		}
	} else if os.Getenv("BENCHMARKER_TURBOPUFFER_LIVE") != "1" {
		t.Skip("set BENCHMARKER_TURBOPUFFER_LIVE=1 or -turbopuffer-env-file for live tests")
	}
	if *liveRegion != "" {
		t.Setenv("TURBOPUFFER_REGION", *liveRegion)
		t.Setenv("TURBOPUFFER_URL", "")
	}
}

// This test writes only to a unique namespace and removes it on completion.
func TestLiveTurbopufferLifecycle(t *testing.T) {
	configureLiveTest(t)
	driver, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	d := driver.(*Driver)
	defer d.Close()
	namespace := fmt.Sprintf("benchmarker-smoke-%d", time.Now().UnixNano())
	t.Logf("temporary namespace: %s", namespace)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := d.request(ctx, http.MethodDelete, namespace, "", nil, true, nil); err != nil {
			t.Errorf("cleanup failed for %s: %v", namespace, err)
		} else {
			t.Logf("deleted temporary namespace: %s", namespace)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	schema := &backends.Schema{Table: namespace, Columns: map[string]string{"id": "bigint", "title": "text", "emb": "vector(2)"}}
	setup := map[string]any{"namespace": namespace, "distance_metric": d.distanceMetric, "schema": map[string]any{
		"id": "uint", "title": map[string]any{"type": "string", "full_text_search": true}, "emb": map[string]any{"type": "[2]f32", "ann": true},
	}}
	pre, err := json.Marshal(setup)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Exec(ctx, string(pre)); err != nil {
		t.Fatal(err)
	}
	cli := backends.NewCLILoader("turbopuffer", "json", "", func(string) (backends.Driver, error) { return d, nil })
	for _, format := range []string{"csv", "parquet", "shards"} {
		dir, path := writeFeatureDataset(t, format, false)
		count, err := cli.Load(ctx, schema, path, 1, 2)
		if err != nil || count != 2 {
			t.Fatalf("CLI %s: %d rows, %v", format, count, err)
		}
		t.Logf("CLI %s: %d rows", format, count)
		if err := os.WriteFile(filepath.Join(dir, "turbopuffer", "pre.json"), pre, 0600); err != nil {
			t.Fatal(err)
		}
		loaded := dataloader.NewLoader(nil).Load("turbopuffer", "", map[string]interface{}{
			"file": path, "schema": filepath.Join(dir, "schema.yaml"), "table": namespace, "dataset": dir, "batchSize": float64(1),
		})
		if loaded["error"] != nil || loaded["loaded"] != 2 {
			t.Fatalf("typed helper %s: %v", format, loaded)
		}
		t.Logf("typed helper %s: 2 rows", format)
	}
	runtime := modulestest.NewRuntime(t)
	vu, rt := runtime.VU, runtime.VU.Runtime()
	if err := rt.Set("backend", backends.NewK6Client(vu, d, "turbopuffer")); err != nil {
		t.Fatal(err)
	}
	if err := rt.Set("namespace", namespace); err != nil {
		t.Fatal(err)
	}
	runtime.MoveToVUContext(&lib.State{Tags: lib.NewVUStateTags(vu.InitEnvField.Registry.RootTagSet()), Samples: make(chan k6metrics.SampleContainer, 100)})
	if _, err := rt.RunString(`
  function rows(r,n){ if(r.error || r.rows!==n) throw Error(JSON.stringify(r)); }
  rows(backend.insert(namespace,{id:3,title:"third",emb:[1,1]}),1);
  rows(backend.insertBatch(namespace,[{id:4,title:"fourth",emb:[1,0]},{id:5,title:"fifth",emb:[0,1]}]),2);
  rows(backend.update(namespace,{id:3,title:"updated",emb:[0,1]}),1);
  rows(backend.updateBatch(namespace,[{id:4,title:"updated",emb:[0,1]},{id:5,title:"updated",emb:[1,0]}]),2);
 `); err != nil {
		t.Fatal(err)
	}
	t.Log("k6 insert, insertBatch, update, updateBatch: passed")
	if err := d.waitForIndex(ctx, namespace, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]any{
		"vector": map[string]any{"rank_by": []any{"emb", "ANN", []float64{1, 0}}, "top_k": 2},
		"BM25":   map[string]any{"rank_by": []any{"title", "BM25", "updated"}, "top_k": 10},
		"count":  map[string]any{"aggregate_by": map[string]any{"count": []string{"Count"}}},
	} {
		count, err := d.Query(ctx, namespace, body)
		want := map[string]int{"vector": 2, "BM25": 3, "count": 5}[name]
		if err != nil || count != want {
			t.Fatalf("%s: %d hits, want %d: %v", name, count, want, err)
		}
		t.Logf("%s: %d hits", name, count)
	}
	var result struct {
		Rows []struct {
			ID    int       `json:"id"`
			Title string    `json:"title"`
			Emb   []float32 `json:"emb"`
		} `json:"rows"`
	}
	if err := d.request(ctx, http.MethodPost, namespace, "/query", map[string]any{
		"rank_by": []string{"id", "asc"}, "filters": []any{"id", "Eq", 3}, "top_k": 1, "include_attributes": true,
	}, false, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || result.Rows[0].Title != "updated" || len(result.Rows[0].Emb) != 2 || result.Rows[0].Emb[1] != 1 {
		t.Fatalf("update did not persist: %+v", result)
	}
	t.Log("read-back verified updated text and vector")
}

var liveCohereDir = flag.String("turbopuffer-cohere-dir", "", "Cohere 1M dataset downloaded with loader pull")
var liveKeep = flag.Bool("turbopuffer-keep", false, "Keep the Cohere namespace after the test, including on failure")

func TestLiveCohere1M(t *testing.T) {
	if *liveCohereDir == "" {
		t.Skip("set -turbopuffer-cohere-dir to test the downloaded Cohere 1M dataset")
	}
	configureLiveTest(t)
	driver, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	d := driver.(*Driver)
	defer d.Close()
	namespace := fmt.Sprintf("benchmarker-cohere-1m-%d", time.Now().UnixNano())
	if *liveNamespace != "" {
		namespace = *liveNamespace
	}
	dir, err := os.MkdirTemp(filepath.Dir(*liveCohereDir), "turbopuffer-run-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("namespace: %s; run dataset: %s", namespace, dir)
	t.Cleanup(func() {
		if *liveKeep {
			t.Logf("retained namespace %s; dataset %s", namespace, dir)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := d.request(ctx, http.MethodDelete, namespace, "", nil, true, nil); err != nil {
			t.Errorf("cleanup %s: %v", namespace, err)
		} else {
			t.Logf("deleted namespace %s", namespace)
		}
	})
	data, err := os.ReadFile(filepath.Join(*liveCohereDir, "schema.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var schema backends.Schema
	if err := yaml.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	schema.Table = namespace
	data, err = yaml.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(*liveCohereDir, "data"), filepath.Join(dir, "data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "turbopuffer"), 0750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pre.json", "post.json"} {
		data, err := os.ReadFile(filepath.Join(*liveCohereDir, "turbopuffer", name))
		if err != nil {
			t.Fatal(err)
		}
		data = []byte(strings.ReplaceAll(string(data), `"cohere_wiki"`, strconv.Quote(namespace)))
		if *liveNamespace != "" {
			data = []byte(strings.ReplaceAll(string(data), `"reset": true`, `"reset": false`))
		}
		if err := os.WriteFile(filepath.Join(dir, "turbopuffer", name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	state := map[string]any{"namespace": namespace, "dataset": dir, "endpoint": d.address, "status": "loading"}
	saveState := func() {
		data, _ := json.MarshalIndent(state, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "run.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	saveState()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "../../bin/loader", "load", "--backend", "turbopuffer", "--workers", "4", "--batch-size", "1000", dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	start := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("loader load: %v", err)
	}
	state["load_and_index_seconds"] = time.Since(start).Seconds()
	count, err := d.Query(ctx, namespace, map[string]any{"aggregate_by": map[string]any{"count": []string{"Count"}}})
	if err != nil || count != 1000000 {
		t.Fatalf("count=%d, want 1000000: %v", count, err)
	}
	state["rows"] = count
	t.Logf("verified %d documents", count)
	vectorsData, err := os.ReadFile(filepath.Join(*liveCohereDir, "k6/query_vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []string
	if err := json.Unmarshal(vectorsData, &vectors); err != nil {
		t.Fatal(err)
	}
	var vector []float64
	if err := json.Unmarshal([]byte(vectors[0]), &vector); err != nil {
		t.Fatal(err)
	}
	for _, filtered := range []bool{false, true} {
		query := map[string]any{"rank_by": []any{"emb", "ANN", vector}, "top_k": 10, "include_attributes": []string{"title"}}
		if filtered {
			query["filters"] = []string{"text", "ContainsAllTokens", "battle"}
		}
		n, err := d.Query(ctx, namespace, query)
		if err != nil || n != 10 {
			t.Fatalf("filtered=%t: %d hits: %v", filtered, n, err)
		}
		t.Logf("vector query filtered=%t: %d hits", filtered, n)
	}
	// Verify mutation paths on a copy of one corpus row, leaving original IDs intact.
	source, err := backends.OpenRowSource(filepath.Join(dir, "data"), &schema)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	row, err := source.Next()
	if err != nil {
		t.Fatal(err)
	}
	cols := source.Columns()
	keyIndex, titleIndex := -1, -1
	for i, col := range cols {
		if col == "_id" {
			keyIndex = i
		}
		if col == "title" {
			titleIndex = i
		}
	}
	if keyIndex < 0 || titleIndex < 0 {
		t.Fatal("missing corpus columns")
	}
	row[keyIndex] = "benchmarker-audit-insert"
	row[titleIndex] = "benchmarker inserted"
	if n, err := d.Insert(ctx, namespace, cols, [][]any{row}); err != nil || n != 1 {
		t.Fatalf("insert: %d %v", n, err)
	}
	row[titleIndex] = "benchmarker updated"
	if n, err := d.Update(ctx, namespace, []string{"_id"}, cols, [][]any{row}); err != nil || n != 1 {
		t.Fatalf("update: %d %v", n, err)
	}
	var result struct {
		Rows []struct {
			Title string `json:"title"`
		} `json:"rows"`
	}
	if err := d.request(ctx, http.MethodPost, namespace, "/query", map[string]any{"rank_by": []string{"id", "asc"}, "filters": []string{"id", "Eq", "benchmarker-audit-insert"}, "top_k": 1, "include_attributes": []string{"title"}}, false, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || result.Rows[0].Title != "benchmarker updated" {
		t.Fatalf("update read-back: %+v", result)
	}
	if err := d.request(ctx, http.MethodPost, namespace, "", map[string]any{"deletes": []string{"benchmarker-audit-insert"}, "distance_metric": "cosine_distance"}, false, nil); err != nil {
		t.Fatal(err)
	}
	t.Log("insert/update/read-back passed; extra audit row removed")
	state["status"] = "passed"
	saveState()
}

var liveNamespace = flag.String("turbopuffer-namespace", "", "Namespace to reuse for Cohere loading or query diagnostics")

func TestLiveNamespaceStatus(t *testing.T) {
	if *liveNamespace == "" {
		t.Skip("set -turbopuffer-namespace")
	}
	configureLiveTest(t)
	driver, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	var metadata json.RawMessage
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := driver.(*Driver).request(ctx, http.MethodGet, *liveNamespace, "/metadata", nil, false, &metadata); err != nil {
		t.Fatal(err)
	}
	t.Log(string(metadata))
}

var liveReportDir = flag.String("turbopuffer-report-dir", "", "Directory for Cohere k6 results")

func TestLiveCohereQueries(t *testing.T) {
	if *liveNamespace == "" || *liveCohereDir == "" || *liveReportDir == "" {
		t.Skip("set namespace, Cohere dataset, and report directory flags")
	}
	configureLiveTest(t)
	driver, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	d := driver.(*Driver)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	vectorsData, err := os.ReadFile(filepath.Join(*liveCohereDir, "k6/query_vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []string
	if err := json.Unmarshal(vectorsData, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) == 0 {
		t.Fatal("no query vectors")
	}
	for i, rawVector := range vectors {
		var vector []float64
		if err := json.Unmarshal([]byte(rawVector), &vector); err != nil {
			t.Fatal(err)
		}
		body := map[string]any{"rank_by": []any{"emb", "ANN", vector}, "top_k": 10, "include_attributes": []string{"title"}}
		// Exercise both measured query shapes before k6 starts. These requests
		// warm the workload without contributing samples to k6 latency metrics.
		if n, err := d.Query(ctx, *liveNamespace, body); err != nil || n != 10 {
			t.Fatalf("unfiltered warmup query %d: %d hits, %v", i+1, n, err)
		}
		body["filters"] = []string{"text", "ContainsAllTokens", "battle"}
		if n, err := d.Query(ctx, *liveNamespace, body); err != nil || n != 10 {
			t.Fatalf("filtered warmup query %d: %d hits, %v", i+1, n, err)
		}
	}
	t.Logf("completed %d unmeasured warmup queries across both query shapes", 2*len(vectors))
	if err := os.MkdirAll(*liveReportDir, 0750); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(*liveCohereDir, "k6", "turbopuffer.js"))
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(*liveReportDir, "turbopuffer.js")
	if err := os.WriteFile(scriptPath, script, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*liveReportDir, "query_vectors.json"), vectorsData, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TURBOPUFFER_NAMESPACE", *liveNamespace)
	t.Setenv("DURATION", "30s")
	t.Setenv("VUS", "1")
	cmd := exec.CommandContext(ctx, "../../k6", "run", "--summary-export", filepath.Join(*liveReportDir, "k6-summary.json"), scriptPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("k6 benchmark: %v", err)
	}
	t.Logf("saved k6 results to %s", *liveReportDir)
}
