package search

import "testing"

func newTestTimer(duration, gap int) *Timer {
	return &Timer{durationSec: duration, gapSec: gap}
}

func TestGetAutoAdvancesOnFirstCall(t *testing.T) {
	timer := newTestTimer(30, 5)
	got := timer.Get()
	if got != "0s" {
		t.Fatalf("first Get() = %q, want %q", got, "0s")
	}
	if !timer.started {
		t.Fatal("timer should be started after Get()")
	}
}

func TestGetReturnsSamePhaseWithoutAdvancing(t *testing.T) {
	timer := newTestTimer(30, 5)
	timer.Get() // first phase "0s"
	got := timer.Get()
	if got != "0s" {
		t.Fatalf("second Get() = %q, want %q", got, "0s")
	}
}

func TestAdvanceAndGetMovesToNextPhase(t *testing.T) {
	timer := newTestTimer(30, 5)
	timer.Get() // "0s"
	got := timer.AdvanceAndGet()
	if got != "35s" {
		t.Fatalf("AdvanceAndGet() = %q, want %q", got, "35s")
	}
}

func TestNextIsAliasForAdvanceAndGet(t *testing.T) {
	timer := newTestTimer(30, 5)
	timer.Get() // "0s"
	got := timer.Next()
	if got != "35s" {
		t.Fatalf("Next() = %q, want %q", got, "35s")
	}
}

func TestMultiplePhases(t *testing.T) {
	timer := newTestTimer(30, 2)

	// Phase 1: two parallel scenarios
	if got := timer.Get(); got != "0s" {
		t.Fatalf("phase 1 first = %q, want %q", got, "0s")
	}
	if got := timer.Get(); got != "0s" {
		t.Fatalf("phase 1 second = %q, want %q", got, "0s")
	}

	// Phase 2
	if got := timer.AdvanceAndGet(); got != "32s" {
		t.Fatalf("phase 2 = %q, want %q", got, "32s")
	}

	// Phase 3
	if got := timer.AdvanceAndGet(); got != "64s" {
		t.Fatalf("phase 3 = %q, want %q", got, "64s")
	}
	// Parallel in phase 3
	if got := timer.Get(); got != "64s" {
		t.Fatalf("phase 3 parallel = %q, want %q", got, "64s")
	}
}

func TestTotalDurationBeforeStart(t *testing.T) {
	timer := newTestTimer(30, 5)
	if got := timer.TotalDuration(); got != "0s" {
		t.Fatalf("TotalDuration() before start = %q, want %q", got, "0s")
	}
}

func TestTotalDurationSinglePhase(t *testing.T) {
	timer := newTestTimer(30, 5)
	timer.Get() // "0s"
	if got := timer.TotalDuration(); got != "35s" {
		t.Fatalf("TotalDuration() = %q, want %q", got, "35s")
	}
}

func TestTotalDurationMultiplePhases(t *testing.T) {
	timer := newTestTimer(30, 5)
	timer.Get()            // "0s"
	timer.AdvanceAndGet()  // "35s"
	timer.AdvanceAndGet()  // "70s"
	if got := timer.TotalDuration(); got != "105s" {
		t.Fatalf("TotalDuration() = %q, want %q", got, "105s")
	}
}

func TestZeroGap(t *testing.T) {
	timer := newTestTimer(30, 0)
	if got := timer.Get(); got != "0s" {
		t.Fatalf("phase 1 = %q, want %q", got, "0s")
	}
	if got := timer.AdvanceAndGet(); got != "30s" {
		t.Fatalf("phase 2 = %q, want %q", got, "30s")
	}
	if got := timer.TotalDuration(); got != "60s" {
		t.Fatalf("TotalDuration() = %q, want %q", got, "60s")
	}
}
