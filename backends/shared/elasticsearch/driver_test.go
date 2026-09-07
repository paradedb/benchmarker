package elasticsearch

import (
	"strings"
	"testing"
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
