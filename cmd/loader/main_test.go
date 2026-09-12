package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/paradedb/benchmarker/backends"
)

func TestParseS3URL(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		wantBucket string
		wantPrefix string
		wantErr    bool
	}{
		{
			name:       "bucket and prefix",
			url:        "s3://my-bucket/datasets/sample/",
			wantBucket: "my-bucket",
			wantPrefix: "datasets/sample",
		},
		{
			name:       "bucket only",
			url:        "s3://my-bucket",
			wantBucket: "my-bucket",
			wantPrefix: "",
		},
		{
			name:    "invalid scheme",
			url:     "https://example.com/bucket",
			wantErr: true,
		},
		{
			name:    "missing bucket",
			url:     "s3://",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			bucket, prefix, err := parseS3URL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.url)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bucket != tt.wantBucket {
				t.Fatalf("bucket mismatch: got %q, want %q", bucket, tt.wantBucket)
			}
			if prefix != tt.wantPrefix {
				t.Fatalf("prefix mismatch: got %q, want %q", prefix, tt.wantPrefix)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{bytes: 0, want: "0 B"},
		{bytes: 1023, want: "1023 B"},
		{bytes: 1024, want: "1.0 KB"},
		{bytes: 1536, want: "1.5 KB"},
		{bytes: 1048576, want: "1.0 MB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.bytes)
		if got != tt.want {
			t.Fatalf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestResolveDownloadPath(t *testing.T) {
	destDir := filepath.Join(string(filepath.Separator), "tmp", "datasets", "sample")

	tests := []struct {
		name    string
		prefix  string
		key     string
		wantRel string
		wantErr bool
	}{
		{
			name:    "nested path",
			prefix:  "datasets/sample",
			key:     "datasets/sample/k6/script.js",
			wantRel: filepath.Join("k6", "script.js"),
		},
		{
			name:    "path traversal",
			prefix:  "datasets/sample",
			key:     "datasets/sample/../../etc/passwd",
			wantErr: true,
		},
		{
			name:    "absolute-like key",
			prefix:  "",
			key:     "/etc/passwd",
			wantErr: true,
		},
		{
			name:    "prefix collision",
			prefix:  "datasets/sample",
			key:     "datasets/sample2/k6/script.js",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rel, local, err := resolveDownloadPath(destDir, tt.prefix, tt.key)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for key %q", tt.key)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rel != tt.wantRel {
				t.Fatalf("rel mismatch: got %q, want %q", rel, tt.wantRel)
			}
			wantLocal := filepath.Join(destDir, tt.wantRel)
			if local != wantLocal {
				t.Fatalf("local mismatch: got %q, want %q", local, wantLocal)
			}
		})
	}
}

func TestLocateDataFilePreference(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.Mkdir(dataDir, 0755); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	csvPath := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(csvPath, []byte("id\n1\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	parquetPath := filepath.Join(dir, "data.parquet")
	if err := os.WriteFile(parquetPath, []byte("PAR1"), 0644); err != nil {
		t.Fatalf("write parquet placeholder: %v", err)
	}

	got, err := locateDataFile(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != parquetPath {
		t.Fatalf("locateDataFile() = %q, want %q", got, parquetPath)
	}
}

func TestLocateDataFileFallsBackToShardDirectory(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.Mkdir(dataDir, 0755); err != nil {
		t.Fatalf("create data dir: %v", err)
	}

	got, err := locateDataFile(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dataDir {
		t.Fatalf("locateDataFile() = %q, want %q", got, dataDir)
	}
}

func TestLocateDataFileRejectsMissingData(t *testing.T) {
	if _, err := locateDataFile(t.TempDir()); err == nil {
		t.Fatal("expected error for dataset without data")
	}
}

func TestLocateTableDataFilePreference(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	shardDir := filepath.Join(dataDir, "posts")
	if err := os.MkdirAll(shardDir, 0755); err != nil {
		t.Fatalf("create shard dir: %v", err)
	}
	csvPath := filepath.Join(dataDir, "posts.csv")
	if err := os.WriteFile(csvPath, []byte("id\n1\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	parquetPath := filepath.Join(dataDir, "posts.parquet")
	if err := os.WriteFile(parquetPath, []byte("PAR1"), 0644); err != nil {
		t.Fatalf("write parquet placeholder: %v", err)
	}

	got, err := locateTableDataFile(dir, "posts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != parquetPath {
		t.Fatalf("locateTableDataFile() = %q, want %q", got, parquetPath)
	}

	if err := os.Remove(parquetPath); err != nil {
		t.Fatalf("remove parquet: %v", err)
	}
	got, err = locateTableDataFile(dir, "posts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != csvPath {
		t.Fatalf("locateTableDataFile() = %q, want %q", got, csvPath)
	}

	if err := os.Remove(csvPath); err != nil {
		t.Fatalf("remove csv: %v", err)
	}
	got, err = locateTableDataFile(dir, "posts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != shardDir {
		t.Fatalf("locateTableDataFile() = %q, want %q", got, shardDir)
	}
}

func TestLocateTableDataFileRejectsMissingTable(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "data"), 0755); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	if _, err := locateTableDataFile(dir, "posts"); err == nil {
		t.Fatal("expected error for missing table data")
	}
}

func TestResolveTableDataMultiTable(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.Mkdir(dataDir, 0755); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	for _, name := range []string{"posts.parquet", "comments.csv"} {
		if err := os.WriteFile(filepath.Join(dataDir, name), []byte("x"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	cols := map[string]string{"id": "bigint"}
	schema := &backends.Schema{Tables: []backends.Schema{
		{Table: "posts", Columns: cols},
		{Table: "comments", Columns: cols},
	}}

	tables, err := resolveTableData(dir, schema)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(tables))
	}
	if tables[0].label != "posts" || tables[0].path != filepath.Join(dataDir, "posts.parquet") {
		t.Fatalf("unexpected posts entry: %+v", tables[0])
	}
	if tables[1].label != "comments" || tables[1].path != filepath.Join(dataDir, "comments.csv") {
		t.Fatalf("unexpected comments entry: %+v", tables[1])
	}
	if tables[0].schema.Table != "posts" || tables[1].schema.Table != "comments" {
		t.Fatal("table schemas not aligned with entries")
	}

	// A table without data fails fast.
	schema.Tables = append(schema.Tables, backends.Schema{Table: "users", Columns: cols})
	if _, err := resolveTableData(dir, schema); err == nil {
		t.Fatal("expected error for table without data")
	}
}
