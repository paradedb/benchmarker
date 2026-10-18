//go:build integration

package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/pgvector/pgvector-go"
)

func TestPostgresVectorLoadAndQuerySmoke(t *testing.T) {
	connString := os.Getenv("BENCHMARKER_POSTGRES_VECTOR_URL")
	if connString == "" {
		t.Skip("set BENCHMARKER_POSTGRES_VECTOR_URL to run the Postgres vector smoke test")
	}

	driver, err := New(connString)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer driver.Close()

	ctx := context.Background()
	table := fmt.Sprintf("benchmarker_vector_smoke_%d", time.Now().UnixNano())
	defer driver.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", table))

	if err := driver.Exec(ctx, fmt.Sprintf(`
		CREATE EXTENSION IF NOT EXISTS vector;
		DROP TABLE IF EXISTS %s CASCADE;
		CREATE TABLE %s (
			id BIGINT PRIMARY KEY,
			emb VECTOR(3)
		);
	`, table, table)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := driver.(*Driver).ReloadTypes(ctx); err != nil {
		t.Fatalf("reload types: %v", err)
	}

	rows := [][]any{
		{int64(1), pgvector.NewVector([]float32{1, 0, 0})},
		{int64(2), pgvector.NewVector([]float32{0, 1, 0})},
		{int64(3), pgvector.NewVector([]float32{0, 0, 1})},
	}
	n, err := driver.Insert(ctx, table, []string{"id", "emb"}, rows)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if n != len(rows) {
		t.Fatalf("inserted %d rows, want %d", n, len(rows))
	}

	hits, err := driver.Query(ctx,
		fmt.Sprintf("SELECT id FROM %s ORDER BY emb <=> $1::vector(3) LIMIT 2", table),
		pgvector.NewVector([]float32{1, 0, 0}),
	)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if hits != 2 {
		t.Fatalf("query returned %d hits, want 2", hits)
	}
}
