//go:build integration

package profiles_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/paradedb/benchmarker/backends/shared/postgres"
	"github.com/paradedb/benchmarker/metrics"
	"github.com/paradedb/benchmarker/profiles"
)

func TestVectorSearchLoadIndexQueryAndCapture(t *testing.T) {
	connString := os.Getenv("BENCHMARKER_VECTORSEARCH_URL")
	if connString == "" {
		t.Skip("set BENCHMARKER_VECTORSEARCH_URL to run the pg_vectorsearch smoke test")
	}
	profile, err := profiles.Builtin("vectorsearch")
	if err != nil {
		t.Fatal(err)
	}
	driver, err := profile.NewDriver(connString)
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	pgDriver := driver.(*postgres.Driver)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	table := fmt.Sprintf("benchmarker_vs_%d", time.Now().UnixNano())
	defer driver.Exec(context.Background(), "DROP TABLE IF EXISTS "+table)
	if err := driver.Exec(ctx, fmt.Sprintf(`
		CREATE EXTENSION IF NOT EXISTS vector;
		CREATE EXTENSION IF NOT EXISTS pg_vectorsearch;
		CREATE TABLE %s (id BIGINT PRIMARY KEY, emb vector(3) STORAGE PLAIN);
	`, table)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := pgDriver.ReloadTypes(ctx); err != nil {
		t.Fatal(err)
	}

	// Capture before the first PRISM query, including on a fresh connection.
	driver.CaptureConfig(ctx, table)
	config := metrics.GetBackendConfig(table)
	if version, ok := config["vectorsearch.version"].(string); !ok || version == "" {
		t.Fatalf("extension version missing: %v", config)
	}
	settings, ok := config["prism"].(map[string]string)
	if !ok || settings["prism.nprobe"] == "" {
		t.Fatalf("PRISM settings missing: %v", config)
	}

	rows := make([][]any, 1000)
	for i := range rows {
		rows[i] = []any{int64(i), []float32{float32(i), float32(i % 7), float32(i % 5)}}
	}
	if n, err := driver.Insert(ctx, table, []string{"id", "emb"}, rows); err != nil || n != len(rows) {
		t.Fatalf("COPY: rows=%d err=%v", n, err)
	}
	if err := driver.Exec(ctx, fmt.Sprintf(`
		CREATE INDEX %s_idx ON %s USING prism (emb vec32_l2_ops);
		VACUUM ANALYZE %s;
		SET enable_seqscan = off;
	`, table, table, table)); err != nil {
		t.Fatalf("index: %v", err)
	}
	query := "SELECT id FROM " + table + " ORDER BY emb <-> $1::vector(3) LIMIT 10"
	if hits, err := driver.Query(ctx, query, "[3,3,3]"); err != nil || hits != 10 {
		t.Fatalf("query: hits=%d err=%v", hits, err)
	}
	plan, err := pgDriver.Pool().Query(ctx, "EXPLAIN (COSTS OFF) "+query, "[3,3,3]")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for plan.Next() {
		var line string
		if err := plan.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	plan.Close()
	if err := plan.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), table+"_idx") {
		t.Fatalf("PRISM index not used: %v", lines)
	}
	if n, err := driver.Update(ctx, table, []string{"id"}, []string{"id", "emb"}, [][]any{
		{int64(0), []float32{-1, -1, -1}},
	}); err != nil || n != 1 {
		t.Fatalf("update: rows=%d err=%v", n, err)
	}
	var nearest int64
	if err := pgDriver.Pool().QueryRow(ctx,
		"SELECT id FROM "+table+" ORDER BY emb <-> '[-1,-1,-1]'::vector LIMIT 1").Scan(&nearest); err != nil || nearest != 0 {
		t.Fatalf("updated nearest neighbor: id=%d err=%v", nearest, err)
	}
	points, err := pgDriver.ReadTelemetry(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range points {
		if point.Name == "index_io.hit_bytes" {
			return
		}
	}
	t.Fatal("index I/O telemetry missing")
}
