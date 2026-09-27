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
