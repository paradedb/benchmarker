package backends

import "testing"

func TestConvertValuePreservesEmptyText(t *testing.T) {
	got := convertValue("", "text")
	if got != "" {
		t.Fatalf("expected empty text value to stay empty, got %#v", got)
	}
}

func TestConvertValueKeepsEmptyIntegerAsNil(t *testing.T) {
	got := convertValue("", "integer")
	if got != nil {
		t.Fatalf("expected empty integer value to become nil, got %#v", got)
	}
}
