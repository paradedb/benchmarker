//go:build integration

package paradedb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/pgvector/pgvector-go"
)

func TestParadeDBVectorLoadIndexAndQuerySmoke(t *testing.T) {
	connString := os.Getenv("BENCHMARKER_PARADEDB_VECTOR_URL")
	if connString == "" {
		t.Skip("set BENCHMARKER_PARADEDB_VECTOR_URL to run the ParadeDB vector smoke test")
	}

	driver, err := New(connString)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer driver.Close()

	ctx := context.Background()
	table := fmt.Sprintf("benchmarker_pdb_vector_smoke_%d", time.Now().UnixNano())
	index := table + "_idx"
	defer driver.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", table))

	if err := driver.Exec(ctx, fmt.Sprintf(`
		CREATE EXTENSION IF NOT EXISTS pg_search;
		CREATE EXTENSION IF NOT EXISTS vector;
		DROP TABLE IF EXISTS %s CASCADE;
		CREATE TABLE %s (
			_id TEXT,
			emb VECTOR(3)
		);
	`, table, table)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if reloader, ok := driver.(interface{ ReloadTypes(context.Context) error }); ok {
		if err := reloader.ReloadTypes(ctx); err != nil {
			t.Fatalf("reload types: %v", err)
		}
	}

	rows := [][]any{
		{"doc-1", pgvector.NewVector([]float32{1, 0, 0})},
		{"doc-2", pgvector.NewVector([]float32{0, 1, 0})},
		{"doc-3", pgvector.NewVector([]float32{0, 0, 1})},
	}
	n, err := driver.Insert(ctx, table, []string{"_id", "emb"}, rows)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if n != len(rows) {
		t.Fatalf("inserted %d rows, want %d", n, len(rows))
	}

	if err := driver.Exec(ctx, fmt.Sprintf(`
		CREATE INDEX %s ON %s
		USING bm25 (_id, emb vector_cosine_ops)
		WITH (key_field = '_id');
		VACUUM ANALYZE %s;
	`, index, table, table)); err != nil {
		t.Fatalf("index: %v", err)
	}

	hits, err := driver.Query(ctx,
		fmt.Sprintf(`
			SELECT _id
			FROM %s
			WHERE _id @@@ paradedb.all()
			ORDER BY emb <=> $1::vector(3)
			LIMIT 2
		`, table),
		pgvector.NewVector([]float32{1, 0, 0}),
	)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if hits != 2 {
		t.Fatalf("query returned %d hits, want 2", hits)
	}
}
