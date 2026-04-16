package elastic

import "testing"

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
