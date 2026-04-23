package search

import (
	"fmt"
	"time"

	"go.k6.io/k6/js/common"
)

// Timer manages scenario timing for sequential benchmark phases.
// Create with search.timer({ duration: "30s", gap: "2s" }).
type Timer struct {
	durationSec int
	gapSec      int
	phaseStart  int
	nextPhase   int
	started     bool
}

// newTimer creates a Timer from a JS config object.
func (m *ModuleInstance) newTimer(config map[string]interface{}) *Timer {
	durationStr, _ := config["duration"].(string)
	gapStr, _ := config["gap"].(string)

	duration, err := time.ParseDuration(durationStr)
	if err != nil || duration <= 0 {
		common.Throw(m.vu.Runtime(), fmt.Errorf("timer: invalid duration %q", durationStr))
		return nil
	}

	gap, _ := time.ParseDuration(gapStr)
	if gap < 0 {
		gap = 0
	}

	return &Timer{
		durationSec: int(duration.Seconds()),
		gapSec:      int(gap.Seconds()),
	}
}

// AdvanceAndGet advances to the next phase and returns the startTime string.
func (t *Timer) AdvanceAndGet() string {
	t.phaseStart = t.nextPhase
	t.nextPhase = t.phaseStart + t.durationSec + t.gapSec
	t.started = true
	return fmt.Sprintf("%ds", t.phaseStart)
}

// Next is an alias for AdvanceAndGet.
func (t *Timer) Next() string {
	return t.AdvanceAndGet()
}

// Get returns the current phase startTime without advancing.
// Use for parallel scenarios that share a phase with the preceding advanceAndGet.
// Auto-advances on first call if no phase has been started.
func (t *Timer) Get() string {
	if !t.started {
		return t.AdvanceAndGet()
	}
	return fmt.Sprintf("%ds", t.phaseStart)
}

// TotalDuration returns the total covering duration as a string (e.g. "70s").
func (t *Timer) TotalDuration() string {
	if !t.started {
		return "0s"
	}
	return fmt.Sprintf("%ds", t.nextPhase)
}
