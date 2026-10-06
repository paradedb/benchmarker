package backends

import (
	"strings"
	"testing"
)

func TestTableSchemasSingleTable(t *testing.T) {
	schema := &Schema{Table: "documents", Columns: map[string]string{"id": "bigint"}}

	got := schema.TableSchemas()
	if len(got) != 1 {
		t.Fatalf("expected 1 table schema, got %d", len(got))
	}
	if got[0] != schema {
		t.Fatal("single-table schema should return itself")
	}
}

func TestTableSchemasMultiTable(t *testing.T) {
	schema := &Schema{Tables: []Schema{
		{Table: "posts", Columns: map[string]string{"id": "bigint"}},
		{Table: "comments", Columns: map[string]string{"id": "bigint"}},
	}}

	got := schema.TableSchemas()
	if len(got) != 2 {
		t.Fatalf("expected 2 table schemas, got %d", len(got))
	}
	if got[0].Table != "posts" || got[1].Table != "comments" {
		t.Fatalf("unexpected table order: %q, %q", got[0].Table, got[1].Table)
	}
}

func TestSchemaValidate(t *testing.T) {
	cols := map[string]string{"id": "bigint"}

	tests := []struct {
		name    string
		schema  Schema
		wantErr string
	}{
		{
			name:   "single table",
			schema: Schema{Table: "documents", Columns: cols},
		},
		{
			name: "multi table",
			schema: Schema{Tables: []Schema{
				{Table: "posts", Columns: cols},
				{Table: "comments", Columns: cols},
			}},
		},
		{
			name:    "mixed forms",
			schema:  Schema{Table: "documents", Tables: []Schema{{Table: "posts", Columns: cols}}},
			wantErr: "cannot mix",
		},
		{
			name:    "unnamed table",
			schema:  Schema{Tables: []Schema{{Columns: cols}}},
			wantErr: "no table name",
		},
		{
			name:    "table without columns",
			schema:  Schema{Tables: []Schema{{Table: "posts"}}},
			wantErr: "no columns",
		},
		{
			name: "nested tables",
			schema: Schema{Tables: []Schema{
				{Table: "posts", Columns: cols, Tables: []Schema{{Table: "inner", Columns: cols}}},
			}},
			wantErr: "cannot be nested",
		},
		{
			name: "duplicate table",
			schema: Schema{Tables: []Schema{
				{Table: "posts", Columns: cols},
				{Table: "posts", Columns: cols},
			}},
			wantErr: "duplicate table",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.schema.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSchemaMultiTable(t *testing.T) {
	loader := NewCLILoader("clickhouse", "sql", "", nil)

	schema := &Schema{Tables: []Schema{
		{Table: "posts", Columns: map[string]string{"id": "bigint"}},
		{Table: "comments", Columns: map[string]string{"id": "bigint"}},
	}}
	if err := loader.ValidateSchema(schema); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// A vector column in one table must fail with the table named.
	schema.Tables[1].Columns["emb"] = "vector(4)"
	err := loader.ValidateSchema(schema)
	if err == nil {
		t.Fatal("expected error for vector column on clickhouse")
	}
	if !strings.Contains(err.Error(), `table "comments"`) {
		t.Fatalf("error %q does not name the failing table", err)
	}
}
