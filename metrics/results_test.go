package metrics

import (
	"context"
	"testing"

	"github.com/grafana/sobek"
	"go.k6.io/k6/js/common"
	"go.k6.io/k6/lib"
	k6metrics "go.k6.io/k6/metrics"
)

type fakeVU struct {
	ctx   context.Context
	state *lib.State
}

func (f fakeVU) Context() context.Context {
	if f.ctx != nil {
		return f.ctx
	}
	return context.Background()
}

func (fakeVU) Events() common.Events { return common.Events{} }

func (fakeVU) InitEnv() *common.InitEnvironment { return nil }

func (f fakeVU) State() *lib.State { return f.state }

func (fakeVU) Runtime() *sobek.Runtime { return nil }

func (fakeVU) RegisterCallback() func(func() error) {
	return func(func() error) {}
}

func TestCaptureQueryPatternUsesExplicitBackend(t *testing.T) {
	oldPatterns := QueryPatterns
	QueryPatterns = make(map[string]string)
	defer func() {
		QueryPatterns = oldPatterns
	}()

	registry := k6metrics.NewRegistry()
	state := &lib.State{
		Tags: lib.NewVUStateTags(
			registry.RootTagSet().
				With("scenario", "search_scenario").
				With("chart", "primary"),
		),
	}

	CaptureQueryPattern(fakeVU{state: state}, "paradedb", "SELECT 1")

	got := GetQueryPattern("paradedb", "primary", "search_scenario")
	if got != "SELECT 1" {
		t.Fatalf("expected captured query for explicit backend, got %q", got)
	}
}
