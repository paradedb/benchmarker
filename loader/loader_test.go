package loader

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

func newTestReader(n int) *DocumentReader {
	docs := make([]map[string]interface{}, n)
	for i := range n {
		docs[i] = map[string]interface{}{"id": i}
	}
	return &DocumentReader{
		documents:    docs,
		size:         n,
		counters:     make(map[string]*atomic.Int64),
		swapCounters: make(map[string]*atomic.Int64),
	}
}

func TestNextBatchDefaultPoolSharedAcrossCallers(t *testing.T) {
	r := newTestReader(10)
	b1 := r.NextBatch(3)
	b2 := r.NextBatch(3)

	// Default pool: second call continues where first left off
	if b1[0]["id"] != 0 {
		t.Fatalf("batch 1 start = %v, want 0", b1[0]["id"])
	}
	if b2[0]["id"] != 3 {
		t.Fatalf("batch 2 start = %v, want 3", b2[0]["id"])
	}
}

func TestNextBatchPoolsAreIndependent(t *testing.T) {
	r := newTestReader(10)

	pdb := r.NextBatch(3, "paradedb")
	es := r.NextBatch(3, "elasticsearch")

	// Each pool starts from 0
	if pdb[0]["id"] != 0 {
		t.Fatalf("paradedb start = %v, want 0", pdb[0]["id"])
	}
	if es[0]["id"] != 0 {
		t.Fatalf("elasticsearch start = %v, want 0", es[0]["id"])
	}

	// Advancing paradedb doesn't affect elasticsearch
	pdb2 := r.NextBatch(3, "paradedb")
	es2 := r.NextBatch(3, "elasticsearch")

	if pdb2[0]["id"] != 3 {
		t.Fatalf("paradedb second = %v, want 3", pdb2[0]["id"])
	}
	if es2[0]["id"] != 3 {
		t.Fatalf("elasticsearch second = %v, want 3", es2[0]["id"])
	}
}

func TestNextBatchCyclesAtEnd(t *testing.T) {
	r := newTestReader(5)
	r.NextBatch(4, "pool")
	batch := r.NextBatch(3, "pool")

	// Should wrap: docs 4, 0, 1
	if batch[0]["id"] != 4 {
		t.Fatalf("wrap batch[0] = %v, want 4", batch[0]["id"])
	}
	if batch[1]["id"] != 0 {
		t.Fatalf("wrap batch[1] = %v, want 0", batch[1]["id"])
	}
	if batch[2]["id"] != 1 {
		t.Fatalf("wrap batch[2] = %v, want 1", batch[2]["id"])
	}
}

func TestNextBatchConcurrentVUsDontOverlap(t *testing.T) {
	r := newTestReader(1000)
	const vus = 10
	const batchSize = 100

	seen := make(map[int]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < vus; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			batch := r.NextBatch(batchSize, "backend")
			mu.Lock()
			defer mu.Unlock()
			for _, doc := range batch {
				id := doc["id"].(int)
				if seen[id] {
					t.Errorf("duplicate document id %d", id)
				}
				seen[id] = true
			}
		}()
	}
	wg.Wait()

	if len(seen) != vus*batchSize {
		t.Fatalf("got %d unique docs, want %d", len(seen), vus*batchSize)
	}
}

func TestNextPoolIsIndependentOfDefaultPool(t *testing.T) {
	r := newTestReader(10)

	// Use default pool
	r.NextBatch(5)
	// Use named pool
	named := r.NextBatch(3, "named")

	// Named pool should start from 0, not 5
	if named[0]["id"] != 0 {
		t.Fatalf("named pool start = %v, want 0", named[0]["id"])
	}
}

func TestNextSingleDoc(t *testing.T) {
	r := newTestReader(5)
	got := r.Next("pool")
	if got["id"] != 0 {
		t.Fatalf("Next() = %v, want 0", got["id"])
	}
	got = r.Next("pool")
	if got["id"] != 1 {
		t.Fatalf("Next() = %v, want 1", got["id"])
	}
}

func TestSizeReturnsDocumentCount(t *testing.T) {
	r := newTestReader(42)
	if r.Size() != 42 {
		t.Fatalf("Size() = %d, want 42", r.Size())
	}
}
