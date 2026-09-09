package elasticsearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAggregationHitCountPrefersBuckets(t *testing.T) {
	value := 7.0
	aggregations := map[string]aggregationResult{
		"z_terms": {
			Buckets: []interface{}{"a", "b", "c"},
		},
		"a_cardinality": {
			Value: &value,
		},
	}

	got, ok := aggregationHitCount(aggregations)
	if !ok {
		t.Fatal("expected aggregation hit count to be present")
	}
	if got != 3 {
		t.Fatalf("expected bucket count of 3, got %d", got)
	}
}

func TestAggregationHitCountUsesSortedKeysForSingleValueAggs(t *testing.T) {
	first := 5.0
	second := 9.0
	aggregations := map[string]aggregationResult{
		"z_count": {
			Value: &second,
		},
		"a_count": {
			Value: &first,
		},
	}

	got, ok := aggregationHitCount(aggregations)
	if !ok {
		t.Fatal("expected aggregation hit count to be present")
	}
	if got != 5 {
		t.Fatalf("expected sorted first aggregation value of 5, got %d", got)
	}
}

func TestBuildBulkBodyRoutesIDAndUnwrapsVectors(t *testing.T) {
	body, err := buildBulkBody("cohere_wiki",
		[]string{"_id", "title", "emb"},
		[][]any{
			{"doc-1", "first", []float32{0.5, -1.25}},
			{"doc-2", "second", nil},
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 ndjson lines, got %d: %q", len(lines), body)
	}
	if lines[0] != `{"index":{"_id":"doc-1","_index":"cohere_wiki"}}` {
		t.Fatalf("unexpected action line: %s", lines[0])
	}
	if lines[1] != `{"emb":[0.5,-1.25],"title":"first"}` {
		t.Fatalf("expected unwrapped vector array, got: %s", lines[1])
	}
	if strings.Contains(lines[3], `"_id"`) {
		t.Fatalf("_id must not appear in the document source: %s", lines[3])
	}
}

func TestExecOperationsHonorsPerOpTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer slow.Close()

	drv, err := New(slow.URL, DriverConfig{})
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	d := drv.(*Driver)

	// Default client timeout (15m) tolerates the slow handler.
	if err := d.execOperations(context.Background(), []map[string]interface{}{
		{"endpoint": "_refresh"},
	}); err != nil {
		t.Fatalf("default timeout should succeed: %v", err)
	}

	// A tight per-op timeout must fail against the same handler.
	err = d.execOperations(context.Background(), []map[string]interface{}{
		{"endpoint": "_forcemerge", "timeout": "20ms"},
	})
	if err == nil {
		t.Fatal("expected timeout error for 20ms per-op timeout")
	}

	// A generous per-op timeout succeeds.
	if err := d.execOperations(context.Background(), []map[string]interface{}{
		{"endpoint": "_forcemerge", "timeout": "5s"},
	}); err != nil {
		t.Fatalf("5s per-op timeout should succeed: %v", err)
	}

	// Malformed timeout is an explicit error, not a silent default.
	err = d.execOperations(context.Background(), []map[string]interface{}{
		{"endpoint": "_forcemerge", "timeout": "2 hours"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid timeout") {
		t.Fatalf("expected invalid timeout error, got %v", err)
	}
}
