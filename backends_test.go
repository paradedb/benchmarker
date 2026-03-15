package search

import (
	"context"
	"testing"

	backendpkg "github.com/paradedb/benchmarks/backends"
	"github.com/paradedb/benchmarks/metrics"
)

type stubDriver struct{}

func (stubDriver) Close() error { return nil }

func (stubDriver) Exec(context.Context, string) error { return nil }

func (stubDriver) Query(context.Context, string, ...any) (int, error) { return 0, nil }

func (stubDriver) Insert(context.Context, string, []string, [][]any) (int, error) { return 0, nil }

func (stubDriver) CaptureConfig(context.Context, string) {}

func TestNewBackendsUsesBackendDefaultContainerWhenAliasIsSet(t *testing.T) {
	backendName := "testbackendcfgalias"
	alias := "custom-alias"

	backendpkg.Register(backendName, backendpkg.BackendConfig{
		Factory:     func(string) (backendpkg.Driver, error) { return stubDriver{}, nil },
		FileType:    "sql",
		DefaultConn: "stub://default",
		Container:   "default-container",
	})

	m := &ModuleInstance{}
	b := m.newBackends(map[string]interface{}{
		"backends": []interface{}{
			map[string]interface{}{
				"type":  backendName,
				"alias": alias,
			},
		},
	})
	if b == nil {
		t.Fatal("expected backends configuration to be created")
	}

	opts := metrics.GetBackendOptions(alias)
	if opts == nil {
		t.Fatal("expected backend options to be registered")
	}
	if opts.Container != "default-container" {
		t.Fatalf("expected default container %q, got %q", "default-container", opts.Container)
	}
}
