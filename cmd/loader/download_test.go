package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/paradedb/benchmarker/drivers"
	"gopkg.in/yaml.v3"
)

type fakeDownloads struct {
	files       map[string][]byte
	gets, lists []string
	fail        string
	onGet       func(string)
}

func (f *fakeDownloads) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := aws.ToString(in.Key)
	f.gets = append(f.gets, key)
	if f.onGet != nil {
		f.onGet(key)
	}
	data, ok := f.files[key]
	if !ok || key == f.fail {
		return nil, fmt.Errorf("unavailable: %s", key)
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data))}, nil
}
func (f *fakeDownloads) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	prefix := aws.ToString(in.Prefix)
	f.lists = append(f.lists, prefix)
	keys := []string{}
	for key := range f.files {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := &s3.ListObjectsV2Output{}
	for _, key := range keys {
		out.Contents = append(out.Contents, types.Object{Key: aws.String(key)})
	}
	return out, nil
}
func pullTestDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
func putTestFile(t *testing.T, base, path string, content []byte) {
	t.Helper()
	full := filepath.Join(base, path)
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0600); err != nil {
		t.Fatal(err)
	}
}
func assertFile(t *testing.T, base, path, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(base, path))
	if err != nil || string(data) != want {
		t.Fatalf("%s = %q, %v; want %q", path, data, err, want)
	}
}
func assertNoStages(t *testing.T, base string) {
	t.Helper()
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".pull-") {
			t.Errorf("left staging file %s", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

const dataManifest = "version: 1\ndata:\n  - source: s3://bucket/corpus/\n    kind: prefix\n    destination: .\n"
const queryManifest = "version: 1\nqueries:\n  - source: s3://bucket/query.parquet\n    kind: object\n    destination: query.parquet\n"

func TestDownloadManifestValidation(t *testing.T) {
	for _, input := range []string{dataManifest, queryManifest, strings.Replace(dataManifest, "destination: .", "destination: posts", 1)} {
		if _, err := decodeDownloadManifest([]byte(input)); err != nil {
			t.Fatal(err)
		}
	}
	for name, input := range map[string]string{
		"version":            strings.Replace(dataManifest, "version: 1", "version: 2", 1),
		"unknown field":      dataManifest + "command: echo unsafe\n",
		"unknown nested":     strings.Replace(dataManifest, "kind: prefix", "kind: prefix\n    shell: bad", 1),
		"HTTP":               strings.Replace(dataManifest, "s3://bucket", "https://bucket", 1),
		"kind":               strings.Replace(dataManifest, "kind: prefix", "kind: archive", 1),
		"traversal":          strings.Replace(dataManifest, "destination: .", "destination: ../k6", 1),
		"internal traversal": strings.Replace(dataManifest, "destination: .", "destination: foo/../bar", 1),
		"absolute":           strings.Replace(dataManifest, "destination: .", "destination: /tmp/foo", 1),
		"windows":            strings.Replace(dataManifest, "destination: .", "destination: C:\\foo", 1),
		"empty":              "version: 1\n",
		"multiple documents": dataManifest + "---\nversion: 1\n",
		"object directory":   strings.Replace(dataManifest, "kind: prefix", "kind: object", 1),
		"compressed output":  strings.ReplaceAll(strings.Replace(queryManifest, "query.parquet", "query.csv.gz", 1), "destination: query.parquet", "destination: query.csv.gz"),
		"duplicate YAML":     dataManifest + "version: 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeDownloadManifest([]byte(input)); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
}

func TestPullCheckoutAndRepeatedPull(t *testing.T) {
	dir := pullTestDir(t)
	manifest := dataManifest + strings.TrimPrefix(queryManifest, "version: 1\n")
	putTestFile(t, dir, "demo/download.yaml", []byte(manifest))
	putTestFile(t, dir, "demo/schema.yaml", []byte("original"))
	putTestFile(t, dir, "demo/dataset.yaml", []byte("s3: s3://old/config/\npulled_at: old-time\ncustom: retained\n"))
	fake := &fakeDownloads{files: map[string][]byte{"corpus/data.csv.gz": testGzip(t, []byte("id\n1\n")), "query.parquet": []byte("PAR1")}}
	if err := pullDataset(context.Background(), fake, dir, "demo", "", 1<<20, nil); err != nil {
		t.Fatal(err)
	}
	assertFile(t, dir, "demo/data/data.csv", "id\n1\n")
	assertFile(t, dir, "demo/queries/query.parquet", "PAR1")
	assertFile(t, dir, "demo/schema.yaml", "original")
	assertFile(t, dir, "demo/download.yaml", manifest)
	raw, err := os.ReadFile(filepath.Join(dir, "demo/dataset.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var provenance map[string]any
	if err := yaml.Unmarshal(raw, &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance["s3"] != "s3://old/config/" || provenance["pulled_at"] != "old-time" || provenance["custom"] != "retained" || provenance["data"] == nil || provenance["queries"] == nil {
		t.Fatalf("provenance: %s", raw)
	}
	gets := len(fake.gets)
	if err := pullDataset(context.Background(), fake, dir, "demo", "", 1<<20, nil); err == nil {
		t.Fatal("repeated pull succeeded")
	}
	if len(fake.gets) != gets {
		t.Fatal("repeat transferred data")
	}
	assertFile(t, dir, "demo/dataset.yaml", string(raw))
	assertNoStages(t, dir)
}

func TestPullFailureLeavesCheckoutIntact(t *testing.T) {
	for _, failure := range []string{"transfer", "decompression", "provenance", "existing queries"} {
		t.Run(failure, func(t *testing.T) {
			dir := pullTestDir(t)
			manifest := dataManifest + strings.TrimPrefix(queryManifest, "version: 1\n")
			putTestFile(t, dir, "demo/download.yaml", []byte(manifest))
			putTestFile(t, dir, "demo/schema.yaml", []byte("keep"))
			fake := &fakeDownloads{files: map[string][]byte{"corpus/data.csv.gz": testGzip(t, []byte("id\n1\n")), "query.parquet": []byte("PAR1")}}
			limit := int64(1 << 20)
			switch failure {
			case "transfer":
				fake.fail = "query.parquet"
			case "decompression":
				limit = 1
			case "provenance":
				putTestFile(t, dir, "demo/dataset.yaml", []byte("[broken"))
			case "existing queries":
				putTestFile(t, dir, "demo/queries/user.txt", []byte("keep"))
			}
			if err := pullDataset(context.Background(), fake, dir, "demo", "", limit, nil); err == nil {
				t.Fatal("expected failure")
			}
			if _, err := os.Stat(filepath.Join(dir, "demo/data")); !os.IsNotExist(err) {
				t.Fatalf("partial data published: %v", err)
			}
			assertFile(t, dir, "demo/schema.yaml", "keep")
			assertFile(t, dir, "demo/download.yaml", manifest)
			if failure == "existing queries" {
				assertFile(t, dir, "demo/queries/user.txt", "keep")
			}
			assertNoStages(t, dir)
		})
	}
}

func archiveFiles(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for path, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: path, Mode: 0600, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
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
func TestPullConfigPackages(t *testing.T) {
	for _, archive := range []bool{false, true} {
		for _, layout := range []string{"embedded", "remote", "legacy", "nested legacy", "mixed", "queries only", "transfer failure"} {
			t.Run(fmt.Sprintf("archive=%t/%s", archive, layout), func(t *testing.T) {
				dir := pullTestDir(t)
				files := map[string][]byte{"schema.yaml": []byte("table: documents\ncolumns: {id: bigint}\n")}
				switch layout {
				case "embedded":
					files["data/data.csv"] = []byte("id\n1\n")
				case "remote", "transfer failure":
					files["download.yaml"] = []byte(dataManifest)
				case "legacy":
					files["data.csv"] = []byte("legacy")
				case "nested legacy":
					files["other/data.parquet"] = []byte("legacy")
				case "mixed":
					files["data/embedded.parquet"] = []byte("PAR1")
					files["download.yaml"] = []byte(dataManifest)
				case "queries only":
					files["data/data.csv"] = []byte("id\n1\n")
					files["download.yaml"] = []byte(queryManifest)
				}
				fake := &fakeDownloads{files: map[string][]byte{"corpus/data.csv": []byte("id\n1\n"), "query.parquet": []byte("PAR1")}}
				source := "s3://bucket/config/"
				if archive {
					source = "s3://bucket/config.tgz"
					fake.files["config.tgz"] = archiveFiles(t, files)
				} else {
					for path, data := range files {
						fake.files["config/"+path] = data
					}
				}
				if layout == "transfer failure" {
					fake.fail = "corpus/data.csv"
				}
				err := pullDataset(context.Background(), fake, dir, "demo", source, 1<<20, nil)
				failure := layout == "legacy" || layout == "nested legacy" || layout == "mixed" || layout == "transfer failure"
				if failure {
					if err == nil {
						t.Fatal("expected error")
					}
					if _, err := os.Stat(filepath.Join(dir, "demo")); !os.IsNotExist(err) {
						t.Fatal("published failed package")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					assertFile(t, dir, "demo/data/data.csv", "id\n1\n")
				}
				if layout == "embedded" || layout == "queries only" || layout == "mixed" {
					for _, key := range fake.gets {
						if strings.HasPrefix(key, "corpus/") {
							t.Fatal("requested second corpus")
						}
					}
				}
				assertNoStages(t, dir)
			})
		}
	}
}

func TestExpandedOutputCollisionsAndTraversal(t *testing.T) {
	for _, keys := range [][]string{{"corpus/x.csv", "corpus/x.csv.gz"}, {"corpus/a", "corpus/a/b"}, {"corpus/../k6/x"}, {"corpus/foo/../x"}, {"corpus//absolute"}} {
		t.Run(strings.Join(keys, ","), func(t *testing.T) {
			dir := pullTestDir(t)
			putTestFile(t, dir, "demo/download.yaml", []byte(dataManifest))
			fake := &fakeDownloads{files: map[string][]byte{}}
			for _, key := range keys {
				fake.files[key] = []byte("x")
			}
			if err := pullDataset(context.Background(), fake, dir, "demo", "", 1<<20, nil); err == nil {
				t.Fatal("expected failure")
			}
			if len(fake.gets) > 0 {
				t.Fatal("transferred before validating outputs")
			}
			assertNoStages(t, dir)
		})
	}
	// Different sources with the same explicit output collide too.
	manifest := queryManifest + "  - source: s3://other/another.parquet\n    kind: object\n    destination: query.parquet\n"
	dir := pullTestDir(t)
	putTestFile(t, dir, "demo/download.yaml", []byte(manifest))
	fake := &fakeDownloads{}
	if err := pullDataset(context.Background(), fake, dir, "demo", "", 1<<20, nil); err == nil {
		t.Fatal("duplicate explicit destination accepted")
	}
	if len(fake.gets) > 0 {
		t.Fatal("transferred duplicate output")
	}
}

func TestPullContainmentAndExistingConfig(t *testing.T) {
	for _, path := range []string{"datasets", "datasets/demo", "datasets/demo/data", "datasets/demo/download.yaml", "datasets/demo/dataset.yaml"} {
		t.Run(path, func(t *testing.T) {
			dir, outside := pullTestDir(t), pullTestDir(t)
			putTestFile(t, dir, "datasets/demo/download.yaml", []byte(dataManifest))
			target := filepath.Join(dir, path)
			if err := os.RemoveAll(target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, target); err != nil {
				t.Fatal(err)
			}
			fake := &fakeDownloads{files: map[string][]byte{"corpus/data.csv": []byte("x")}}
			if err := pullDataset(context.Background(), fake, filepath.Join(dir, "datasets"), "demo", "", 1<<20, nil); err == nil {
				t.Fatal("symlink accepted")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) > 0 {
				t.Fatal("wrote outside destination")
			}
			assertNoStages(t, dir)
		})
	}
	dir := pullTestDir(t)
	fake := &fakeDownloads{}
	for _, name := range []string{"../escape", "/tmp/escape", "a/b", "a\\b", ".", "..", ""} {
		if err := pullDataset(context.Background(), fake, dir, name, "s3://bucket/config/", 1<<20, nil); err == nil {
			t.Fatalf("accepted name %q", name)
		}
	}
	putTestFile(t, dir, "demo/schema.yaml", []byte("keep"))
	if err := pullDataset(context.Background(), fake, dir, "demo", "s3://bucket/config/", 1<<20, nil); err == nil {
		t.Fatal("accepted nonempty config directory")
	}
	if err := pullDataset(context.Background(), fake, dir, "demo", "", 1<<20, nil); err == nil {
		t.Fatal("accepted missing manifest")
	}
	if len(fake.gets)+len(fake.lists) > 0 {
		t.Fatal("requested data before checking destination")
	}
}

func TestExcludeOnlyConfigPrefix(t *testing.T) {
	dir := pullTestDir(t)
	fake := &fakeDownloads{files: map[string][]byte{"config/download.yaml": []byte(dataManifest), "config/data.csv.gz": []byte("legacy"), "corpus/data.csv.gz": testGzip(t, []byte("id\n1\n"))}}
	if err := pullDataset(context.Background(), fake, dir, "demo", "s3://bucket/config/", 1<<20, []string{"data.csv.gz"}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, dir, "demo/data/data.csv", "id\n1\n")
	for _, source := range []string{"", "s3://bucket/package.tgz"} {
		if err := pullDataset(context.Background(), fake, dir, "other", source, 1<<20, []string{"x"}); err == nil {
			t.Fatal("accepted unsupported exclude")
		}
	}
}

func TestLegacyCorpusRejectedBeforeLoad(t *testing.T) {
	for _, name := range []string{"data.csv", "data.parquet", "data.csv.gz", "posts.csv"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			putTestFile(t, dir, name, []byte("old"))
			putTestFile(t, dir, "data/data.csv", []byte("id\n1\n"))
			putTestFile(t, dir, "data/posts.csv", []byte("id\n1\n"))
			if _, err := locateDataFile(dir); err == nil || !strings.Contains(err.Error(), "under") {
				t.Fatalf("expected migration error: %v", err)
			}
			schema := &drivers.Schema{Tables: []drivers.Schema{{Table: "posts", Columns: map[string]string{"id": "bigint"}}}}
			if _, err := resolveTableData(dir, schema); err == nil {
				t.Fatal("multi-table ignored legacy corpus")
			}
		})
	}
	if _, err := locateDataFile("../../datasets/sample"); err != nil {
		t.Fatalf("sample: %v", err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := locateDataFile(dir); err == nil {
		t.Fatal("accepted empty shard directory")
	}
}

func TestMultiTablePrefixMapping(t *testing.T) {
	dir := pullTestDir(t)
	manifest := "version: 1\ndata:\n  - source: s3://bucket/stackoverflow_posts/\n    kind: prefix\n    destination: posts\n  - source: s3://bucket/users.csv.gz\n    kind: object\n    destination: users.csv\n"
	putTestFile(t, dir, "demo/download.yaml", []byte(manifest))
	fake := &fakeDownloads{files: map[string][]byte{"stackoverflow_posts/part.parquet": []byte("PAR1"), "users.csv.gz": testGzip(t, []byte("id\n1\n"))}}
	if err := pullDataset(context.Background(), fake, dir, "demo", "", 1<<20, nil); err != nil {
		t.Fatal(err)
	}
	schema := &drivers.Schema{Tables: []drivers.Schema{{Table: "posts", Columns: map[string]string{"id": "bigint"}}, {Table: "users", Columns: map[string]string{"id": "bigint"}}}}
	tables, err := resolveTableData(filepath.Join(dir, "demo"), schema)
	if err != nil {
		t.Fatal(err)
	}
	if tables[0].schema.Table != "posts" || tables[0].path != filepath.Join(dir, "demo/data/posts") || tables[1].path != filepath.Join(dir, "demo/data/users.csv") {
		t.Fatalf("bad mapping: %+v", tables)
	}
}

func TestManifestCatalog(t *testing.T) {
	var paths []string
	for _, name := range []string{"cohere-1m", "cohere-10m", "sift-10m", "sift-100m"} {
		paths = append(paths, filepath.Join("../../datasets", name, "download.yaml"))
	}
	packagePaths, err := filepath.Glob("../../dataset-packages/*/download.yaml")
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, packagePaths...)
	if len(paths) != 9 {
		t.Fatalf("expected nine canonical manifests, got %d", len(paths))
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := decodeDownloadManifest(data)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, ref := range append(manifest.Data, manifest.Queries...) {
			if !strings.HasPrefix(ref.Source, "s3://paradedb-benchmark/datasets/") {
				t.Fatalf("noncanonical source in %s: %s", path, ref.Source)
			}
		}
	}
}

// Exercise real prepared configs through both pull flows, replacing corpus and
// query payloads with tiny fake objects. Full-size production corpora are never
// needed to verify package orchestration and table/destination mapping.
func TestPreparedPackages(t *testing.T) {
	sourceDir := os.Getenv("BENCHMARKER_PREPARED_PACKAGES")
	if sourceDir == "" {
		t.Skip("set BENCHMARKER_PREPARED_PACKAGES to prepared package directory")
	}
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		count++
		t.Run(entry.Name(), func(t *testing.T) {
			source := filepath.Join(sourceDir, entry.Name())
			fake := &fakeDownloads{files: map[string][]byte{}}
			err := filepath.WalkDir(source, func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				rel, err := filepath.Rel(source, path)
				if err != nil {
					return err
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				fake.files["config/"+rel] = data
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := decodeDownloadManifest(fake.files["config/download.yaml"])
			if err != nil {
				t.Fatal(err)
			}
			for _, ref := range append(append([]downloadReference{}, manifest.Data...), manifest.Queries...) {
				_, key, err := parseS3URL(ref.Source)
				if err != nil {
					t.Fatal(err)
				}
				if ref.Kind == "prefix" {
					key = s3ListPrefix(key) + "0000.parquet"
				}
				data := []byte("id,body,text\n1,example,example\n")
				if strings.HasSuffix(key, ".parquet") {
					data = []byte("PAR1")
				}
				if isCsvGzPath(key) {
					data = testGzip(t, data)
				}
				fake.files[key] = data
			}
			for _, fresh := range []bool{true, false} {
				dir := pullTestDir(t)
				sourceURL := "s3://bucket/config/"
				if !fresh {
					sourceURL = ""
					for key, data := range fake.files {
						if strings.HasPrefix(key, "config/") {
							putTestFile(t, dir, filepath.Join("demo", strings.TrimPrefix(key, "config/")), data)
						}
					}
				}
				if err := pullDataset(context.Background(), fake, dir, "demo", sourceURL, 1<<20, nil); err != nil {
					t.Fatal(err)
				}
				dataset := filepath.Join(dir, "demo")
				schema, err := loadSchema(dataset)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := resolveTableData(dataset, schema); err != nil {
					t.Fatal(err)
				}
				assertNoStages(t, dir)
			}
		})
	}
	if count != 9 {
		t.Fatalf("expected nine packages, got %d", count)
	}
}

func TestConfigPrefixCollisionBeforeTransfer(t *testing.T) {
	dir := pullTestDir(t)
	fake := &fakeDownloads{files: map[string][]byte{"config/data/x.csv": []byte("x"), "config/data/x.csv.gz": testGzip(t, []byte("x"))}}
	if err := pullDataset(context.Background(), fake, dir, "demo", "s3://bucket/config/", 1<<20, nil); err == nil {
		t.Fatal("accepted colliding config outputs")
	}
	if len(fake.gets) != 0 {
		t.Fatal("transferred colliding config objects")
	}
	assertNoStages(t, dir)
}

func TestArchivePackageRejectsUnsafeEntries(t *testing.T) {
	for _, path := range []string{"../escape", "/absolute", "data/../../escape", "data/a/../b"} {
		t.Run(path, func(t *testing.T) {
			dir := pullTestDir(t)
			fake := &fakeDownloads{files: map[string][]byte{"config.tgz": archiveFiles(t, map[string][]byte{path: []byte("unsafe")})}}
			if err := pullDataset(context.Background(), fake, dir, "demo", "s3://bucket/config.tgz", 1<<20, nil); err == nil {
				t.Fatal("accepted unsafe archive entry")
			}
			assertNoStages(t, dir)
			if _, err := os.Stat(filepath.Join(dir, "demo")); !os.IsNotExist(err) {
				t.Fatal("published unsafe archive")
			}
		})
	}
}

func TestObjectCompressionFollowsSource(t *testing.T) {
	dir := pullTestDir(t)
	manifest := "version: 1\nqueries:\n  - source: s3://bucket/raw\n    kind: object\n    destination: original.csv.gz\n"
	putTestFile(t, dir, "demo/download.yaml", []byte(manifest))
	fake := &fakeDownloads{files: map[string][]byte{"raw": []byte("unchanged")}}
	if err := pullDataset(context.Background(), fake, dir, "demo", "", 1<<20, nil); err != nil {
		t.Fatal(err)
	}
	assertFile(t, dir, "demo/queries/original.csv.gz", "unchanged")
}

func TestPublicationFailureRollsBackCompletedDirectories(t *testing.T) {
	dir := pullTestDir(t)
	manifest := dataManifest + strings.TrimPrefix(queryManifest, "version: 1\n")
	putTestFile(t, dir, "demo/download.yaml", []byte(manifest))
	putTestFile(t, dir, "demo/dataset.yaml", []byte("s3: s3://original/config/\n"))
	fake := &fakeDownloads{files: map[string][]byte{"corpus/data.csv": []byte("id\n1\n"), "query.parquet": []byte("PAR1")}}
	fake.onGet = func(key string) {
		if key == "query.parquet" {
			putTestFile(t, dir, "demo/queries/user.txt", []byte("keep"))
		}
	}
	if err := pullDataset(context.Background(), fake, dir, "demo", "", 1<<20, nil); err == nil {
		t.Fatal("overwrote destination created during transfer")
	}
	if _, err := os.Stat(filepath.Join(dir, "demo/data")); !os.IsNotExist(err) {
		t.Fatal("left partially published data directory")
	}
	assertFile(t, dir, "demo/queries/user.txt", "keep")
	assertFile(t, dir, "demo/dataset.yaml", "s3: s3://original/config/\n")
	assertNoStages(t, dir)
}
