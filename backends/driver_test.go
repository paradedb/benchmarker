package backends

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grafana/sobek"
	"go.k6.io/k6/js/common"
	"go.k6.io/k6/lib"
	"go.k6.io/k6/lib/executor"
	"go.k6.io/k6/lib/types"
	k6metrics "go.k6.io/k6/metrics"
	"gopkg.in/guregu/null.v3"
)

func TestConvertValuePreservesEmptyText(t *testing.T) {
	got, err := convertValue("", "text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty text value to stay empty, got %#v", got)
	}
}

func TestConvertValueKeepsEmptyIntegerAsNil(t *testing.T) {
	got, err := convertValue("", "integer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected empty integer value to become nil, got %#v", got)
	}
}

func TestConvertValueRejectsInvalidInteger(t *testing.T) {
	_, err := convertValue("abc", "integer")
	if err == nil {
		t.Fatal("expected error for invalid integer")
	}
}

func TestConvertValueSmallint(t *testing.T) {
	got, err := convertValue("123", "smallint")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := got.(int16); !ok || v != 123 {
		t.Fatalf("expected int16 123, got %#v", got)
	}

	if _, err := convertValue("40000", "smallint"); err == nil {
		t.Fatal("expected error for smallint overflow")
	}
	if _, err := convertValue("abc", "int2"); err == nil {
		t.Fatal("expected error for invalid smallint")
	}
}

func TestConvertValueRejectsInvalidJSONArray(t *testing.T) {
	_, err := convertValue("not-json", "text[]")
	if err == nil {
		t.Fatal("expected error for invalid JSON array")
	}
}

func TestConvertValueRejectsInvalidBoolean(t *testing.T) {
	_, err := convertValue("maybe", "boolean")
	if err == nil {
		t.Fatal("expected error for invalid boolean")
	}
}

type recordingDriver struct {
	inserted int
}

func (d *recordingDriver) Close() error                                       { return nil }
func (d *recordingDriver) Exec(context.Context, string) error                 { return nil }
func (d *recordingDriver) Query(context.Context, string, ...any) (int, error) { return 0, nil }
func (d *recordingDriver) CaptureConfig(context.Context, string)              {}
func (d *recordingDriver) Insert(_ context.Context, _ string, _ []string, rows [][]any) (int, error) {
	d.inserted += len(rows)
	return len(rows), nil
}
func (d *recordingDriver) Update(_ context.Context, _ string, _ []string, _ []string, rows [][]any) (int, error) {
	return len(rows), nil
}

type driverFakeVU struct {
	ctx   context.Context
	state *lib.State
}

func (v driverFakeVU) Context() context.Context           { return v.ctx }
func (driverFakeVU) Events() common.Events                { return common.Events{} }
func (driverFakeVU) InitEnv() *common.InitEnvironment     { return nil }
func (v driverFakeVU) State() *lib.State                  { return v.state }
func (driverFakeVU) Runtime() *sobek.Runtime              { return nil }
func (driverFakeVU) RegisterCallback() func(func() error) { return func(func() error) {} }

func warmupDriverVU(start time.Time, firstStage time.Duration) driverFakeVU {
	registry := k6metrics.NewRegistry()
	config := executor.NewRampingVUsConfig("search")
	config.Stages = []executor.Stage{
		{Duration: types.NewNullDuration(firstStage, true), Target: null.NewInt(1, true)},
		{Duration: types.NewNullDuration(time.Second, true), Target: null.NewInt(1, true)},
	}
	state := &lib.State{
		Tags: lib.NewVUStateTags(
			registry.RootTagSet().
				With("scenario", "search").
				With("warmup", "true"),
		),
		Options: lib.Options{Scenarios: lib.ScenarioConfigs{"search": config}},
	}
	return driverFakeVU{
		ctx: lib.WithScenarioState(context.Background(), &lib.ScenarioState{
			Name:      "search",
			StartTime: start,
		}),
		state: state,
	}
}

type deadlineDriver struct {
	deadline chan time.Time
}

func (d *deadlineDriver) wait(ctx context.Context) (int, error) {
	deadline, _ := ctx.Deadline()
	d.deadline <- deadline
	<-ctx.Done()
	return 0, ctx.Err()
}

func (d *deadlineDriver) Close() error                       { return nil }
func (d *deadlineDriver) Exec(context.Context, string) error { return nil }
func (d *deadlineDriver) Query(ctx context.Context, _ string, _ ...any) (int, error) {
	return d.wait(ctx)
}
func (d *deadlineDriver) CaptureConfig(context.Context, string) {}
func (d *deadlineDriver) Insert(ctx context.Context, _ string, _ []string, _ [][]any) (int, error) {
	return d.wait(ctx)
}
func (d *deadlineDriver) Update(ctx context.Context, _ string, _ []string, _ []string, _ [][]any) (int, error) {
	return d.wait(ctx)
}

func TestWarmupOperationsShareFirstStageDeadline(t *testing.T) {
	tests := []struct {
		name string
		run  func(*K6Client) map[string]interface{}
	}{
		{name: "query", run: func(client *K6Client) map[string]interface{} {
			return client.Query("SELECT 1")
		}},
		{name: "insert", run: func(client *K6Client) map[string]interface{} {
			return client.Insert("documents", map[string]interface{}{"id": 1})
		}},
		{name: "update", run: func(client *K6Client) map[string]interface{} {
			return client.Update("documents", map[string]interface{}{"id": 1, "title": "updated"})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const firstStage = 200 * time.Millisecond
			start := time.Now().Add(-50 * time.Millisecond)
			driver := &deadlineDriver{deadline: make(chan time.Time, 1)}
			result := tt.run(NewK6Client(warmupDriverVU(start, firstStage), driver, "paradedb"))

			deadline := <-driver.deadline
			wantDeadline := start.Add(firstStage)
			if delta := deadline.Sub(wantDeadline); delta < -time.Millisecond || delta > time.Millisecond {
				t.Fatalf("deadline = %s, want %s", deadline, wantDeadline)
			}
			if reached, _ := result["deadlineReached"].(bool); !reached {
				t.Fatalf("result = %#v, want deadlineReached", result)
			}
			if _, exists := result["error"]; exists {
				t.Fatalf("boundary cancellation surfaced as query error: %#v", result)
			}
		})
	}
}

func TestOperationStartingAfterWarmupIsMeasuredNormally(t *testing.T) {
	vu := warmupDriverVU(time.Now().Add(-2*time.Second), time.Second)
	result := NewK6Client(vu, &recordingDriver{}, "paradedb").Query("SELECT 1")
	if _, exists := result["deadlineReached"]; exists {
		t.Fatalf("second-stage operation used warmup deadline: %#v", result)
	}
	if _, exists := result["error"]; exists {
		t.Fatalf("second-stage operation failed: %#v", result)
	}
}

func TestSchemaColumnsInOrderRejectsMissingColumns(t *testing.T) {
	_, err := schemaColumnsInOrder(&Schema{
		Columns: map[string]string{"id": "text", "title": "text", "content": "text"},
	}, []string{"id", "title"})
	if err == nil {
		t.Fatal("expected error for missing schema column")
	}
	if !strings.Contains(err.Error(), "content") {
		t.Fatalf("expected missing column name in error, got %v", err)
	}
}

func TestCLILoaderLoadFailsOnInvalidTypedValue(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(csvPath, []byte("numbers\nnot-json\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	driver := &recordingDriver{}
	loader := NewCLILoader("test", "sql", "stub://default", func(string) (Driver, error) {
		return driver, nil
	})

	_, err := loader.Load(context.Background(), &Schema{
		Table:   "documents",
		Columns: map[string]string{"numbers": "integer[]"},
	}, csvPath, 100, 1)
	if err == nil {
		t.Fatal("expected error for invalid typed value")
	}
	if !strings.Contains(err.Error(), `row 2 column "numbers"`) {
		t.Fatalf("expected row/column context in error, got %v", err)
	}
	if driver.inserted != 0 {
		t.Fatalf("expected no rows inserted, got %d", driver.inserted)
	}
}
