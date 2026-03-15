package search

import (
	"context"
	"reflect"
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

func TestNewBackendsDeduplicatesContainerMetricsCollection(t *testing.T) {
	backendA := "testbackendcontainera"
	backendB := "testbackendcontainerb"

	for _, name := range []string{backendA, backendB} {
		backendpkg.Register(name, backendpkg.BackendConfig{
			Factory:     func(string) (backendpkg.Driver, error) { return stubDriver{}, nil },
			FileType:    "sql",
			DefaultConn: "stub://default",
			Container:   "shared-container",
		})
	}

	m := &ModuleInstance{}
	b := m.newBackends(map[string]interface{}{
		"backends": []interface{}{backendA, backendB},
	})
	if b == nil || b.Metrics == nil {
		t.Fatal("expected metrics collector to be created")
	}

	containers := reflect.ValueOf(b.Metrics).Elem().FieldByName("containers")
	if containers.Len() != 1 {
		t.Fatalf("expected one deduplicated container, got %d", containers.Len())
	}
	if got := containers.Index(0).String(); got != "shared-container" {
		t.Fatalf("expected shared container name to be preserved, got %q", got)
	}
}
