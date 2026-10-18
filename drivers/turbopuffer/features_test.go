package turbopuffer

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/paradedb/benchmarker/drivers"
	dataloader "github.com/paradedb/benchmarker/loader"
	"github.com/parquet-go/parquet-go"
	"go.k6.io/k6/js/modulestest"
	"go.k6.io/k6/lib"
	k6metrics "go.k6.io/k6/metrics"
)

type recordedWrite struct {
	Namespace string
	Rows      []map[string]any `json:"upsert_rows"`
	Schema    map[string]any   `json:"schema"`
	Metric    string           `json:"distance_metric"`
}

type featureRecorder struct {
	mu      sync.Mutex
	writes  []recordedWrite
	deletes []string
}

func (f *featureRecorder) serve(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodDelete:
			f.deletes = append(f.deletes, r.URL.Path)
			fmt.Fprint(w, `{}`)
		case r.URL.Path == "/v2/namespaces/documents/query":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["rank_by"] == nil {
				t.Error("lost query body")
			}
			fmt.Fprint(w, `{"rows":[{"id":1},{"id":2}]}`)
		case r.Method == http.MethodPost:
			var write recordedWrite
			decoder := json.NewDecoder(r.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&write); err != nil {
				t.Error(err)
			}
			write.Namespace = r.URL.Path
			f.writes = append(f.writes, write)
			fmt.Fprintf(w, `{"rows_affected":%d}`, len(write.Rows))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(400)
		}
	}
}

func writeFeatureDataset(t *testing.T, format string, largeID bool) (string, string) {
	t.Helper()
	dir := t.TempDir()
	schema := "table: documents\ncolumns:\n  id: bigint\n  title: text\n  emb: vector(2)\n"
	if err := os.WriteFile(filepath.Join(dir, "schema.yaml"), []byte(schema), 0600); err != nil {
		t.Fatal(err)
	}
	type row struct {
		ID    int64     `parquet:"id"`
		Title string    `parquet:"title"`
		Emb   []float32 `parquet:"emb,list"`
	}
	id := int64(1)
	if largeID {
		id = 9007199254740993
	}
	rows := []row{{id, "first", []float32{1, 0}}, {id + 1, "second", []float32{0, 1}}}
	path := filepath.Join(dir, "data."+format)
	switch format {
	case "csv":
		csv := fmt.Sprintf("id,title,emb\n%d,first,\"[1,0]\"\n%d,second,\"[0,1]\"\n", id, id+1)
		if err := os.WriteFile(path, []byte(csv), 0600); err != nil {
			t.Fatal(err)
		}
	case "parquet":
		if err := parquet.WriteFile(path, rows); err != nil {
			t.Fatal(err)
		}
	case "shards":
		path = filepath.Join(dir, "data")
		if err := os.Mkdir(path, 0750); err != nil {
			t.Fatal(err)
		}
		for i, row := range rows {
			if err := parquet.WriteFile(filepath.Join(path, fmt.Sprintf("%02d.parquet", i)), []struct {
				ID    int64     `parquet:"id"`
				Title string    `parquet:"title"`
				Emb   []float32 `parquet:"emb,list"`
			}{row}); err != nil {
				t.Fatal(err)
			}
		}
	}
	backendDir := filepath.Join(dir, "turbopuffer")
	if err := os.Mkdir(backendDir, 0750); err != nil {
		t.Fatal(err)
	}
	pre := `{"namespace":"documents","schema":{"emb":{"type":"[2]f32","ann":true}},"distance_metric":"euclidean_squared"}`
	if err := os.WriteFile(filepath.Join(backendDir, "pre.json"), []byte(pre), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backendDir, "post.json"), []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func TestCLIFormatsPreserveTypes(t *testing.T) {
	for _, format := range []string{"csv", "parquet", "shards"} {
		t.Run(format, func(t *testing.T) {
			recorder := &featureRecorder{}
			d := testDriver(t, recorder.serve(t))
			dir, path := writeFeatureDataset(t, format, true)
			cli := drivers.NewCLILoader("turbopuffer", "json", "", func(string) (drivers.Driver, error) { return d, nil })
			schema := &drivers.Schema{Table: "documents", Columns: map[string]string{"id": "bigint", "title": "text", "emb": "vector(2)"}}
			if err := cli.RunPre(context.Background(), filepath.Join(dir, "turbopuffer"), schema); err != nil {
				t.Fatal(err)
			}
			count, err := cli.Load(context.Background(), schema, path, 1, 3)
			if err != nil || count != 2 {
				t.Fatalf("%d: %v", count, err)
			}
			if err := cli.RunPost(context.Background(), filepath.Join(dir, "turbopuffer"), schema); err != nil {
				t.Fatal(err)
			}
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			seen := map[string]bool{}
			for _, write := range recorder.writes {
				if write.Metric != "euclidean_squared" || write.Schema["emb"] == nil {
					t.Fatalf("lost setup: %+v", write)
				}
				for _, row := range write.Rows {
					id, ok := row["id"].(json.Number)
					if !ok {
						t.Fatalf("ID is %T", row["id"])
					}
					seen[string(id)] = true
					if vector := decodedVector(t, row["emb"]); len(vector) != 2 {
						t.Fatalf("lost vector: %+v", row)
					}
				}
			}
			if !seen["9007199254740993"] || !seen["9007199254740994"] {
				t.Fatalf("ID precision lost: %v", seen)
			}
		})
	}
}

func TestK6JavaScriptFeatures(t *testing.T) {
	recorder := &featureRecorder{}
	d := testDriver(t, recorder.serve(t))
	runtime := modulestest.NewRuntime(t)
	vu, rt := runtime.VU, runtime.VU.Runtime()
	client := drivers.NewK6Client(vu, d, "tpuf-features")
	if err := rt.Set("backend", client); err != nil {
		t.Fatal(err)
	}
	samples := make(chan k6metrics.SampleContainer, 100)
	runtime.MoveToVUContext(&lib.State{Tags: lib.NewVUStateTags(vu.InitEnvField.Registry.RootTagSet()), Samples: samples})
	if _, err := rt.RunString(`
  function rows(result,n) { if(result.error || result.rows!==n) throw Error(JSON.stringify(result)); }
  rows(backend.insert("documents", {id:1,title:"one"}),1);
  rows(backend.insertBatch("documents", [{id:2,title:"two"},{id:3,title:"three",extra:"retained"}]),2);
  rows(backend.update("documents", {id:1,title:"changed"}),1);
  rows(backend.updateBatch("documents", [{id:2,title:"changed"},{id:3,title:"changed",extra:"updated"}]),2);
  rows(backend.insertBatch("documents", []),0);
  rows(backend.updateBatch("documents", []),0);
  var result=backend.query("documents",{rank_by:["title","BM25","changed"],top_k:10});
  if(result.error || result.hits!==2) throw Error(JSON.stringify(result));
 `); err != nil {
		t.Fatal(err)
	}
	recorder.mu.Lock()
	if len(recorder.writes) != 4 || recorder.writes[1].Rows[1]["extra"] != "retained" || recorder.writes[3].Rows[1]["extra"] != "updated" {
		t.Fatalf("lost batch fields: %+v", recorder.writes)
	}
	recorder.mu.Unlock()
	totals := map[string]float64{}
	for len(samples) > 0 {
		for _, sample := range (<-samples).GetSamples() {
			if tag, ok := sample.Tags.Get("backend"); !ok || tag != "tpuf-features" {
				t.Fatalf("wrong metric tag %q", tag)
			}
			totals[sample.Metric.Name] += sample.Value
		}
	}
	for name, want := range map[string]float64{"ingest_docs": 3, "update_docs": 3, "query_hits": 2} {
		if totals[name] != want {
			t.Fatalf("%s = %v, want %v", name, totals[name], want)
		}
	}
}

func TestK6TypedLoaderAndStreams(t *testing.T) {
	for _, format := range []string{"csv", "parquet", "shards"} {
		t.Run(format, func(t *testing.T) {
			recorder := &featureRecorder{}
			d := testDriver(t, recorder.serve(t))
			d.distanceMetric = "euclidean_squared"
			original, _ := drivers.GetConfig("turbopuffer")
			configured := original
			configured.Factory = func(string) (drivers.Driver, error) { return d.NewLoadWorker() }
			drivers.Register("turbopuffer", configured)
			t.Cleanup(func() { drivers.Register("turbopuffer", original) })
			dir, path := writeFeatureDataset(t, format, false)
			runtime := modulestest.NewRuntime(t)
			vu, rt := runtime.VU, runtime.VU.Runtime()
			vu.InitEnvField.CWD = &url.URL{Scheme: "file", Path: filepath.Join(dir, "k6")}
			if err := rt.Set("loader", dataloader.NewLoader(vu)); err != nil {
				t.Fatal(err)
			}
			if err := rt.Set("dataPath", "../"+filepath.Base(path)); err != nil {
				t.Fatal(err)
			}
			if err := rt.Set("backend", drivers.NewK6Client(vu, d, "tpuf-stream")); err != nil {
				t.Fatal(err)
			}
			if _, err := rt.RunString(`var stream=loader.openDocumentStream(dataPath,{schema:"../schema.yaml"});`); err != nil {
				t.Fatal(err)
			}
			runtime.MoveToVUContext(&lib.State{Tags: lib.NewVUStateTags(vu.InitEnvField.Registry.RootTagSet()), Samples: make(chan k6metrics.SampleContainer, 100)})
			if _, err := rt.RunString(`
   var loaded=loader.load("turbopuffer","",{file:dataPath,schema:"../schema.yaml",dataset:"../",batchSize:1});
   if(loaded.error || loaded.loaded!==2) throw Error(JSON.stringify(loaded));
   var batch=stream.nextBatch(10);
   if(batch.length!==2 || batch[0].emb[0]!==1) throw Error("bad typed data");
   var inserted=backend.insertBatch("documents",batch);
   if(inserted.error || inserted.rows!==2) throw Error(JSON.stringify(inserted));
   var tmp=batch[0].emb;batch[0].emb=batch[1].emb;batch[1].emb=tmp;
   var updated=backend.updateBatch("documents",batch);
   if(updated.error || updated.rows!==2) throw Error(JSON.stringify(updated));
   if(stream.nextBatch(10).length!==0) throw Error("bad EOF");
   stream.close();
  `); err != nil {
				t.Fatal(err)
			}
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			if len(recorder.writes) != 4 {
				t.Fatal(len(recorder.writes))
			}
			if recorder.writes[0].Schema["emb"] == nil || recorder.writes[0].Metric != "euclidean_squared" {
				t.Fatal("typed loader lost pre script")
			}
			for _, write := range recorder.writes {
				for _, row := range write.Rows {
					if _, ok := row["id"].(json.Number); !ok {
						t.Fatalf("numeric ID became %T", row["id"])
					}
					if len(decodedVector(t, row["emb"])) != 2 {
						t.Fatalf("vector became %T", row["emb"])
					}
				}
			}
			if decodedVector(t, recorder.writes[3].Rows[0]["emb"])[1] != 1 {
				t.Fatal("vector update lost")
			}
		})
	}
}

func decodedVector(t *testing.T, value any) []float32 {
	t.Helper()
	if encoded, ok := value.(string); ok {
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(data)%4 != 0 {
			t.Fatalf("invalid binary vector: %v", err)
		}
		result := make([]float32, len(data)/4)
		for i := range result {
			result[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
		}
		return result
	}
	values, ok := value.([]any)
	if !ok {
		t.Fatalf("unexpected vector type %T", value)
	}
	result := make([]float32, len(values))
	for i, value := range values {
		switch n := value.(type) {
		case json.Number:
			f, err := n.Float64()
			if err != nil {
				t.Fatal(err)
			}
			result[i] = float32(f)
		case float64:
			result[i] = float32(n)
		default:
			t.Fatalf("unexpected vector element %T", value)
		}
	}
	return result
}
