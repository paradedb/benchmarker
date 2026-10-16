//go:build integration

package workloads_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/paradedb/benchmarker/backends/shared/postgres"
	"github.com/paradedb/benchmarker/metrics"
	"github.com/paradedb/benchmarker/workloads"
)

func TestVectorChordLoadIndexUpdateAndQuery(t *testing.T) {
	connString := os.Getenv("BENCHMARKER_VECTORCHORD_VECTOR_URL")
	if connString == "" {
		t.Skip("set BENCHMARKER_VECTORCHORD_VECTOR_URL to run the VectorChord smoke test")
	}
	workload, err := workloads.Builtin("vectorchord")
	if err != nil {
		t.Fatal(err)
	}
	driver, err := workload.NewDriver(connString)
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	pgDriver := driver.(*postgres.Driver)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	table := fmt.Sprintf("benchmarker_vchord_smoke_%d", time.Now().UnixNano())
	defer driver.Exec(context.Background(), "DROP TABLE IF EXISTS "+table+" CASCADE")
	if err := driver.Exec(ctx, fmt.Sprintf(`
		CREATE EXTENSION IF NOT EXISTS vchord CASCADE;
		CREATE TABLE %s (id bigint PRIMARY KEY, emb vector(3));
	`, table)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := pgDriver.ReloadTypes(ctx); err != nil {
		t.Fatal(err)
	}

	// Use the neutral vector representation produced by CSV/Parquet loaders.
	rows := [][]any{
		{int64(1), []float32{1, 0, 0}},
		{int64(2), []float32{0, 1, 0}},
		{int64(3), []float32{0, 0, 1}},
	}
	if n, err := driver.Insert(ctx, table, []string{"id", "emb"}, rows); err != nil || n != len(rows) {
		t.Fatalf("insert: rows=%d, err=%v", n, err)
	}
	index := table + "_idx"
	if err := driver.Exec(ctx, fmt.Sprintf(`
		CREATE INDEX %s ON %s USING vchordrq (emb vector_l2_ops);
		VACUUM ANALYZE %s;
		SET enable_seqscan = off;
	`, index, table, table)); err != nil {
		t.Fatalf("index: %v", err)
	}
	query := fmt.Sprintf("SELECT id FROM %s ORDER BY emb <-> $1::vector LIMIT 2", table)
	if hits, err := driver.Query(ctx, query, "[1,0,0]"); err != nil || hits != 2 {
		t.Fatalf("query: hits=%d, err=%v", hits, err)
	}
	var plan string
	if err := pgDriver.Pool().QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+query, "[1,0,0]").Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, index) {
		t.Fatalf("query did not use VectorChord index: %s", plan)
	}
	if n, err := driver.Update(ctx, table, []string{"id"}, []string{"id", "emb"},
		[][]any{{int64(1), []float32{2, 0, 0}}}); err != nil || n != 1 {
		t.Fatalf("update: rows=%d, err=%v", n, err)
	}
	if hits, err := driver.Query(ctx, "SELECT id FROM "+table+" WHERE emb = $1::vector", "[2,0,0]"); err != nil || hits != 1 {
		t.Fatalf("updated vector: hits=%d, err=%v", hits, err)
	}

	driver.CaptureConfig(ctx, table)
	config := metrics.GetBackendConfig(table)
	versions, ok := config["vectorchord"].(map[string]string)
	if !ok || versions["vectorchord.vchord_version"] == "" || versions["vectorchord.vector_version"] == "" {
		t.Fatalf("missing extension versions: %#v", config)
	}
	if settings, ok := config["vchordrq"].(map[string]string); !ok || len(settings) == 0 {
		t.Fatalf("missing VectorChord settings: %#v", config)
	}
	points, err := pgDriver.ReadTelemetry(ctx)
	if err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	for _, point := range points {
		if point.Name == "index_io.hit_bytes" {
			return
		}
	}
	t.Fatal("missing index I/O telemetry")
}
