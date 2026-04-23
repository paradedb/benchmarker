package search

import "testing"

func newTestTerms(terms ...string) *Terms {
	return &Terms{terms: terms}
}

func TestNextCyclesSequentially(t *testing.T) {
	terms := newTestTerms("a", "b", "c")
	want := []string{"a", "b", "c", "a", "b"}
	for i, w := range want {
		got := terms.Next()
		if got != w {
			t.Fatalf("Next() call %d = %q, want %q", i, got, w)
		}
	}
}

func TestNextSingleTerm(t *testing.T) {
	terms := newTestTerms("only")
	for i := 0; i < 5; i++ {
		if got := terms.Next(); got != "only" {
			t.Fatalf("Next() = %q, want %q", got, "only")
		}
	}
}

func TestRandomReturnsValidTerm(t *testing.T) {
	terms := newTestTerms("a", "b", "c")
	valid := map[string]bool{"a": true, "b": true, "c": true}
	for i := 0; i < 100; i++ {
		got := terms.Random()
		if !valid[got] {
			t.Fatalf("Random() = %q, not in term list", got)
		}
	}
}

func TestNextIndependentPerInstance(t *testing.T) {
	// Simulates two VUs each getting their own Terms instance
	vu1 := newTestTerms("a", "b", "c")
	vu2 := newTestTerms("a", "b", "c")

	// Both should start from the same position
	if got := vu1.Next(); got != "a" {
		t.Fatalf("vu1 first = %q, want %q", got, "a")
	}
	if got := vu2.Next(); got != "a" {
		t.Fatalf("vu2 first = %q, want %q", got, "a")
	}

	// Advancing vu1 doesn't affect vu2
	vu1.Next() // "b"
	vu1.Next() // "c"
	if got := vu2.Next(); got != "b" {
		t.Fatalf("vu2 second = %q, want %q", got, "b")
	}
}
