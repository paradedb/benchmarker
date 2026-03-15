package backends

import (
	"context"
	"testing"
	"time"

	"github.com/grafana/sobek"
	"go.k6.io/k6/js/common"
	"go.k6.io/k6/lib"
)

type clientTestVU struct {
	ctx context.Context
}

func (f clientTestVU) Context() context.Context {
	if f.ctx != nil {
		return f.ctx
	}
	return context.Background()
}

func (clientTestVU) Events() common.Events { return common.Events{} }

func (clientTestVU) InitEnv() *common.InitEnvironment { return nil }

func (clientTestVU) State() *lib.State { return nil }

func (clientTestVU) Runtime() *sobek.Runtime { return nil }

func (clientTestVU) RegisterCallback() func(func() error) {
	return func(func() error) {}
}

type blockingDriver struct{}

func (blockingDriver) Close() error { return nil }

func (blockingDriver) Exec(context.Context, string) error { return nil }

func (blockingDriver) CaptureConfig(context.Context, string) {}

func (blockingDriver) Query(ctx context.Context, _ string, _ ...any) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(200 * time.Millisecond):
		return 1, nil
	}
}

func (blockingDriver) Insert(ctx context.Context, _ string, _ []string, _ [][]any) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(200 * time.Millisecond):
		return 1, nil
	}
}

func TestSearchUsesVUContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := NewK6Client(clientTestVU{ctx: ctx}, blockingDriver{}, "paradedb")

	start := time.Now()
	result := client.Search("SELECT 1")
	elapsed := time.Since(start)

	if elapsed >= 100*time.Millisecond {
		t.Fatalf("expected canceled VU context to stop search quickly, took %s", elapsed)
	}
	if result["error"] == nil {
		t.Fatal("expected canceled search to return an error")
	}
}

func TestInsertBatchUsesVUContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := NewK6Client(clientTestVU{ctx: ctx}, blockingDriver{}, "paradedb")

	start := time.Now()
	result := client.InsertBatch("documents", []map[string]interface{}{
		{"id": 1},
	})
	elapsed := time.Since(start)

	if elapsed >= 100*time.Millisecond {
		t.Fatalf("expected canceled VU context to stop insert quickly, took %s", elapsed)
	}
	if result["error"] == nil {
		t.Fatal("expected canceled insert to return an error")
	}
}
