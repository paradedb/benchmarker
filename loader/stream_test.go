package loader

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/paradedb/benchmarker/backends"
	"github.com/parquet-go/parquet-go"
	"go.k6.io/k6/js/modulestest"
	"go.k6.io/k6/lib"
	k6metrics "go.k6.io/k6/metrics"
)

func streamFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	schema := filepath.Join(dir, "schema.yaml")
	if err := os.WriteFile(schema, []byte("table: documents\ncolumns:\n  id: bigint\n  title: text\n  emb: vector(3)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	type row struct {
		ID    int64     `parquet:"id"`
		Title *string   `parquet:"title,optional"`
		Emb   []float32 `parquet:"emb,list"`
	}
	title := "first"
	if err := parquet.WriteFile(filepath.Join(dir, "data.parquet"), []row{
		{1, &title, []float32{1, 0, 0}},
		{2, nil, []float32{0, 1, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data.csv"), []byte("id,title,emb\n1,first,\"[1,0,0]\"\n2,,\"[0,1,0]\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir, schema
}

func TestDocumentStreamBatches(t *testing.T) {
	type document struct {
		ID    int64  `parquet:"id"`
		Title string `parquet:"title"`
	}
	rows := []document{{1, "first"}, {2, "second"}, {3, "third"}, {4, "fourth"}}
	for _, sharded := range []bool{false, true} {
		t.Run(fmt.Sprintf("sharded=%t", sharded), func(t *testing.T) {
			dir := t.TempDir()
			schema := filepath.Join(dir, "schema.yaml")
			if err := os.WriteFile(schema, []byte("columns:\n  id: bigint\n  title: text\n"), 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "data.parquet")
			if sharded {
				// Write in reverse order to exercise filename ordering and cross-shard batches.
				for i := 1; i >= 0; i-- {
					if err := parquet.WriteFile(filepath.Join(dir, fmt.Sprintf("%04d.parquet", i)), rows[i*2:(i+1)*2]); err != nil {
						t.Fatal(err)
					}
				}
				path = dir
			} else if err := parquet.WriteFile(path, rows); err != nil {
				t.Fatal(err)
			}
			s, err := NewLoader(nil).OpenDocumentStream(path, map[string]interface{}{"schema": schema})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			for i, want := range [][]document{rows[:3], rows[3:], nil, nil} {
				batch, err := s.NextBatch(3)
				if err != nil || len(batch) != len(want) {
					t.Fatalf("batch %d: got %#v, %v; want %d rows", i, batch, err, len(want))
				}
				for j, doc := range want {
					if !reflect.DeepEqual(batch[j], map[string]interface{}{"id": doc.ID, "title": doc.Title}) {
						t.Fatalf("batch %d row %d: %#v", i, j, batch[j])
					}
				}
			}
		})
	}
}

func TestDocumentStreamsAreIndependent(t *testing.T) {
	dir, schema := streamFixture(t)
	l := NewLoader(nil)
	for _, format := range []string{"parquet", "csv"} {
		t.Run(format, func(t *testing.T) {
			open := func() *DocumentStream {
				s, err := l.OpenDocumentStream(filepath.Join(dir, "data."+format), map[string]interface{}{"schema": schema})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = s.Close() })
				return s
			}
			a, b := open(), open()
			first, err := a.NextBatch(1)
			if err != nil {
				t.Fatal(err)
			}
			first[0]["title"] = "mutated"
			first[0]["emb"].([]float32)[0] = 9
			other, err := b.NextBatch(1)
			if err != nil {
				t.Fatal(err)
			}
			want := []map[string]interface{}{{"id": int64(1), "title": "first", "emb": []float32{1, 0, 0}}}
			if !reflect.DeepEqual(other, want) {
				t.Fatalf("independent first batch: %#v", other)
			}
			second, err := a.NextBatch(1)
			if err != nil {
				t.Fatal(err)
			}
			if second[0]["id"] != int64(2) {
				t.Fatalf("second batch: %#v", second)
			}
			if format == "parquet" && second[0]["title"] != nil {
				t.Fatalf("null not preserved: %#v", second)
			}
		})
	}
}

func TestDocumentStreamErrorsAndClose(t *testing.T) {
	dir, schema := streamFixture(t)
	l := NewLoader(nil)
	options := map[string]interface{}{"schema": schema}
	s, err := l.OpenDocumentStream(filepath.Join(dir, "data.parquet"), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, size := range []int{0, -1} {
		if _, err := s.NextBatch(size); err == nil {
			t.Fatalf("accepted batch size %d", size)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := s.NextBatch(1); err == nil {
		t.Fatal("read after close succeeded")
	}
	if _, err := l.OpenDocumentStream(filepath.Join(dir, "missing.parquet"), options); err == nil {
		t.Fatal("missing file accepted")
	}
	if _, err := l.OpenDocumentStream(filepath.Join(dir, "data.parquet"), nil); err == nil {
		t.Fatal("missing schema accepted")
	}
	for _, tc := range []struct{ name, content string }{
		{"bad.parquet", "not parquet"},
		{"bad.csv", "id,title,emb\n1,first,\"[1,0,0]\"\n2,second,\"[1,2]\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := l.OpenDocumentStream(path, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			got, err := s.NextBatch(10)
			if err == nil || !strings.Contains(err.Error(), tc.name) || len(got) != 0 {
				t.Fatalf("expected file-specific failure without partial batch, got %#v, %v", got, err)
			}
		})
	}
}

type streamUpdateDriver struct {
	backends.Driver
	table string
	keys  []string
	docs  []map[string]interface{}
}

func (d *streamUpdateDriver) Update(_ context.Context, table string, keys, cols []string, rows [][]any) (int, error) {
	d.table, d.keys = table, keys
	for _, row := range rows {
		doc := make(map[string]interface{}, len(cols))
		for i, col := range cols {
			doc[col] = row[i]
		}
		d.docs = append(d.docs, doc)
	}
	return len(rows), nil
}

func TestDocumentStreamJavaScriptUpdate(t *testing.T) {
	dir, _ := streamFixture(t)
	runtime := modulestest.NewRuntime(t)
	vu, rt := runtime.VU, runtime.VU.Runtime()
	vu.InitEnvField.CWD = &url.URL{Scheme: "file", Path: filepath.Join(dir, "k6")}
	if err := rt.Set("loader", NewLoader(vu)); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RunString(`var stream = loader.openDocumentStream("../data.parquet", {schema: "../schema.yaml"});`); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RunString(`stream.nextBatch(1)`); err == nil {
		t.Fatal("init-context read accepted")
	}
	driver := &streamUpdateDriver{}
	client := backends.NewK6Client(vu, driver, "postgres")
	if err := rt.Set("backend", client); err != nil {
		t.Fatal(err)
	}
	runtime.MoveToVUContext(&lib.State{
		Tags:    lib.NewVUStateTags(vu.InitEnvField.Registry.RootTagSet()),
		Samples: make(chan k6metrics.SampleContainer, 100),
	})
	if _, err := rt.RunString(`
		var batch = stream.nextBatch(10);
		if (batch.length !== 2 || batch[0].emb[0] !== 1 || batch[1].title !== null) throw Error("bad conversion");
		var tmp = batch[0].emb; batch[0].emb = batch[1].emb; batch[1].emb = tmp;
		var result = backend.updateBatch("documents", batch);
		if (result.error || result.rows !== 2) throw Error("update failed");
		for (let i = 0; i < 2; i++) {
			var eof = stream.nextBatch(10);
			if (!Array.isArray(eof) || eof.length !== 0) throw Error("EOF must be []");
		}
		stream.close(); stream.close();
	`); err != nil {
		t.Fatal(err)
	}
	if driver.table != "documents" || !reflect.DeepEqual(driver.keys, []string{"id"}) {
		t.Fatalf("update target: %s, keys: %v", driver.table, driver.keys)
	}
	want := []map[string]interface{}{
		{"id": int64(1), "title": "first", "emb": []float32{0, 1, 0}},
		{"id": int64(2), "title": nil, "emb": []float32{1, 0, 0}},
	}
	if !reflect.DeepEqual(driver.docs, want) {
		t.Fatalf("update lost IDs, nulls or vector types: %#v", driver.docs)
	}
}

func TestDocumentStreamCanceledReadClosesSource(t *testing.T) {
	dir, schema := streamFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vu := &modulestest.VU{CtxField: ctx, StateField: &lib.State{}}
	s, err := NewLoader(vu).OpenDocumentStream(filepath.Join(dir, "data.parquet"), map[string]interface{}{"schema": schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.NextBatch(1); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := s.NextBatch(1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %v", err)
	}
	if !s.closed || s.source != nil {
		t.Fatal("canceled read did not close the reader")
	}
}

func TestDocumentStreamSurvivesIterations(t *testing.T) {
	dir, schema := streamFixture(t)
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	vu := &modulestest.VU{CtxField: runCtx}
	loader := NewLoader(vu)
	vu.StateField = &lib.State{}
	var stream *DocumentStream
	for iteration := 0; iteration < 2; iteration++ {
		ctx, cancel := context.WithCancel(runCtx)
		vu.CtxField = ctx
		if iteration == 0 {
			// Readers opened during an iteration also survive its completion.
			var err error
			stream, err = loader.OpenDocumentStream(filepath.Join(dir, "data.parquet"), map[string]interface{}{"schema": schema})
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stream.Close() })
		}
		batch, err := stream.NextBatch(1)
		cancel()
		if err != nil || len(batch) != 1 || batch[0]["id"] != int64(iteration+1) {
			t.Fatalf("iteration %d: %#v, %v", iteration, batch, err)
		}
	}
}

func TestDocumentStreamDoesNotReadLaterShardsUntilNeeded(t *testing.T) {
	dir, schema := streamFixture(t)
	if err := os.Rename(filepath.Join(dir, "data.parquet"), filepath.Join(dir, "0000.parquet")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "0001.parquet"), []byte("corrupt later shard"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewLoader(nil).OpenDocumentStream(dir, map[string]interface{}{"schema": schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	batch, err := s.NextBatch(1)
	if err != nil || len(batch) != 1 || batch[0]["id"] != int64(1) {
		t.Fatalf("first shard should be readable before opening later shards: %#v, %v", batch, err)
	}
	batch, err = s.NextBatch(2)
	if err == nil || !strings.Contains(err.Error(), "0001.parquet") || len(batch) != 0 {
		t.Fatalf("expected later-shard error without partial batch: %#v, %v", batch, err)
	}
}

func TestLegacyOpenDocumentsStillCachesStringsAndCycles(t *testing.T) {
	dir, _ := streamFixture(t)
	path := filepath.Join(dir, "data.csv")
	a, b := NewLoader(nil).OpenDocuments(path), NewLoader(nil).OpenDocuments(path)
	if a != b {
		t.Fatal("legacy CSV cache no longer shared")
	}
	batch := a.NextBatch(3)
	if len(batch) != 3 || batch[0]["id"] != "1" || batch[1]["id"] != "2" || batch[2]["id"] != "1" {
		t.Fatalf("legacy string values or cycling changed: %#v", batch)
	}
	if batch[0]["emb"] != "[1,0,0]" {
		t.Fatalf("legacy CSV unexpectedly converted embeddings: %#v", batch)
	}
}
