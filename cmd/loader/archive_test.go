package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testArchive(t *testing.T, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		data := strings.Repeat("x", 2048)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0777, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testGzip(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestWritePulledCSVGz(t *testing.T) {
	for _, relPath := range []string{"data.csv.gz", "data/posts.csv.gz"} {
		t.Run(relPath, func(t *testing.T) {
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()

			want := []byte("id,body\n1,hello\n")
			outputPath, n, err := writePulledObject(root, dir, relPath, bytes.NewReader(testGzip(t, want)), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			wantPath := strings.TrimSuffix(relPath, ".gz")
			if outputPath != wantPath {
				t.Fatalf("output path = %q, want %q", outputPath, wantPath)
			}
			if n != int64(len(want)) {
				t.Fatalf("written bytes = %d, want %d", n, len(want))
			}
			got, err := os.ReadFile(filepath.Join(dir, wantPath))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("decompressed data = %q, want %q", got, want)
			}
			if _, err := os.Stat(filepath.Join(dir, relPath)); !os.IsNotExist(err) {
				t.Fatalf("compressed input was materialized: %v", err)
			}
		})
	}
}

func TestWritePulledCSVGzBudgetAndCleanup(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	_, _, err = writePulledObject(root, dir, "data.csv.gz", bytes.NewReader(testGzip(t, bytes.Repeat([]byte("x"), 2048))), 1024)
	if err == nil || !strings.Contains(err.Error(), "decompression limit") {
		t.Fatalf("expected budget error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data.csv")); !os.IsNotExist(err) {
		t.Fatalf("partial output was not removed: %v", err)
	}
}

func TestWritePulledCSVGzRejectsCollision(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.csv"), []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if _, _, err := writePulledObject(root, dir, "data.csv.gz", bytes.NewReader(testGzip(t, []byte("replacement"))), 1<<20); err == nil {
		t.Fatal("expected existing destination error")
	}
	got, err := os.ReadFile(filepath.Join(dir, "data.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing" {
		t.Fatalf("existing data was modified: %q", got)
	}
}

func TestWritePulledCSVGzChecksumAndCleanup(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	data := testGzip(t, []byte("id\n1\n"))
	data[len(data)-8] ^= 0xff
	if _, _, err := writePulledObject(root, dir, "data.csv.gz", bytes.NewReader(data), 1<<20); err == nil {
		t.Fatal("expected checksum error")
	}
	if _, err := os.Stat(filepath.Join(dir, "data.csv")); !os.IsNotExist(err) {
		t.Fatalf("partial output was not removed: %v", err)
	}
}

func TestWritePulledObjectUncompressed(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	want := []byte("table: documents\n")
	outputPath, n, err := writePulledObject(root, dir, "schema.yaml", bytes.NewReader(want), 1)
	if err != nil {
		t.Fatal(err)
	}
	if outputPath != "schema.yaml" || n != int64(len(want)) {
		t.Fatalf("got path %q and %d bytes", outputPath, n)
	}
	got, err := os.ReadFile(filepath.Join(dir, outputPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("data = %q, want %q", got, want)
	}
}

func TestExtractArchive(t *testing.T) {
	dir := t.TempDir()
	if err := extractTarGz(bytes.NewReader(testArchive(t, "nested/data.csv")), dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "nested/data.csv"))
	if err != nil || len(data) != 2048 {
		t.Fatalf("data length %d, error %v", len(data), err)
	}
	for path, forbidden := range map[string]os.FileMode{"nested": 0027, "nested/data.csv": 0177} {
		info, err := os.Stat(filepath.Join(dir, path))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&forbidden != 0 {
			t.Fatalf("unsafe permissions on %s: %v", path, info.Mode())
		}
	}
}

func TestArchiveBudget(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		limit int64
	}{
		{"file", []string{"data.csv"}, 1024},
		{"aggregate", []string{"a", "b"}, 4096},
		{"skipped traversal", []string{"../escape"}, 1024},
		{"tar metadata", []string{strings.Repeat("long/", 300) + "data"}, 1024},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := extractTarGz(bytes.NewReader(testArchive(t, tc.paths...)), t.TempDir(), tc.limit)
			if err == nil || !strings.Contains(err.Error(), "decompression limit") {
				t.Fatalf("expected budget error, got %v", err)
			}
		})
	}
}

func TestArchiveRejectsSymlinkEscape(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "nested")); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGz(bytes.NewReader(testArchive(t, "nested/data.csv")), dir, 1<<20); err == nil {
		t.Fatal("expected symlink escape error")
	}
	if _, err := os.Stat(filepath.Join(outside, "data.csv")); !os.IsNotExist(err) {
		t.Fatalf("outside file: %v", err)
	}
}

func TestArchiveChecksumAndTrailingData(t *testing.T) {
	data := testArchive(t, "data.csv")
	data[len(data)-8] ^= 0xff
	if err := extractTarGz(bytes.NewReader(data), t.TempDir(), 1<<20); err == nil {
		t.Fatal("expected checksum error")
	}
	data = testArchive(t, "data.csv")
	var extra bytes.Buffer
	gz := gzip.NewWriter(&extra)
	if _, err := gz.Write(bytes.Repeat([]byte("x"), 8192)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	data = append(data, extra.Bytes()...)
	if err := extractTarGz(bytes.NewReader(data), t.TempDir(), 8192); err == nil {
		t.Fatal("expected trailing data budget error")
	}
}
