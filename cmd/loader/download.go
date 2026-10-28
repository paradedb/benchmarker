package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.yaml.in/yaml/v3"
)

// download.yaml is declarative configuration. dataset.yaml is generated provenance.
type downloadManifest struct {
	Version int                 `yaml:"version"`
	Data    []downloadReference `yaml:"data,omitempty"`
}
type downloadReference struct {
	Source      string `yaml:"source"`
	Kind        string `yaml:"kind"`
	Destination string `yaml:"destination"`
}
type s3Downloads interface {
	s3.ListObjectsV2APIClient
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}
type downloadObject struct {
	bucket, key, output string
	compressed          bool
}

func validateRelativePath(path string, allowDot bool) error {
	if path == "." && allowDot {
		return nil
	}
	if path == "" || filepath.IsAbs(path) || strings.ContainsAny(path, "\\\x00:") {
		return fmt.Errorf("invalid relative destination %q", path)
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." || part == "." || part == "" {
			return fmt.Errorf("unsafe relative path %q", path)
		}
	}
	return nil
}

func decodeDownloadManifest(data []byte) (*downloadManifest, error) {
	var manifest downloadManifest
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("download.yaml: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("download.yaml must contain exactly one YAML document")
	}
	if manifest.Version != 1 {
		return nil, fmt.Errorf("unsupported download.yaml version %d (expected 1)", manifest.Version)
	}
	if len(manifest.Data) == 0 {
		return nil, fmt.Errorf("download.yaml declares no data")
	}
	for _, ref := range manifest.Data {
		_, key, err := parseS3URL(ref.Source)
		if err != nil {
			return nil, err
		}
		if ref.Kind != "prefix" && ref.Kind != "object" {
			return nil, fmt.Errorf("unsupported download kind %q", ref.Kind)
		}
		if ref.Kind == "object" && (key == "" || strings.HasSuffix(ref.Source, "/")) {
			return nil, fmt.Errorf("object source requires an exact S3 key: %s", ref.Source)
		}
		if err := validateRelativePath(ref.Destination, ref.Kind == "prefix"); err != nil {
			return nil, err
		}
		if ref.Kind == "object" && isCsvGzPath(key) && !strings.HasSuffix(ref.Destination, ".csv") {
			return nil, fmt.Errorf("compressed CSV destination must name the decompressed .csv file")
		}
	}
	return &manifest, nil
}

func readDownloadManifest(root *os.Root, required bool) (*downloadManifest, error) {
	if err := rejectSymlink(root, "download.yaml"); err != nil {
		return nil, err
	}
	data, err := root.ReadFile("download.yaml")
	if os.IsNotExist(err) && !required {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading required download.yaml: %w", err)
	}
	return decodeDownloadManifest(data)
}

func listDownloadObjects(ctx context.Context, client s3Downloads, bucket, prefix string) ([]string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(s3ListPrefix(prefix))})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing s3://%s/%s: %w", bucket, prefix, err)
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if !strings.HasSuffix(key, "/") {
				keys = append(keys, key)
			}
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no objects found at s3://%s/%s", bucket, prefix)
	}
	return keys, nil
}

func expandReference(ctx context.Context, client s3Downloads, ref downloadReference, base string, excludes []string) ([]downloadObject, error) {
	bucket, prefix, err := parseS3URL(ref.Source)
	if err != nil {
		return nil, err
	}
	if ref.Kind == "object" {
		return []downloadObject{{bucket, prefix, filepath.Join(base, ref.Destination), isCsvGzPath(prefix)}}, nil
	}
	keys, err := listDownloadObjects(ctx, client, bucket, prefix)
	if err != nil {
		return nil, err
	}
	var objects []downloadObject
	for _, key := range keys {
		rel, _, err := resolveDownloadPath(".", prefix, key)
		if err != nil {
			return nil, err
		}
		if isExcludedPath(rel, excludes) {
			continue
		}
		output := rel
		if isCsvGzPath(key) {
			output = strings.TrimSuffix(output, ".gz")
		}
		objects = append(objects, downloadObject{bucket, key, filepath.Join(base, ref.Destination, output), isCsvGzPath(key)})
	}
	return objects, nil
}

// Check all expanded names before any GetObject, including file/directory collisions.
func validateOutputs(objects []downloadObject) error {
	names := make([]string, 0, len(objects))
	for _, obj := range objects {
		if err := validateRelativePath(obj.output, false); err != nil {
			return err
		}
		names = append(names, obj.output)
	}
	sort.Strings(names)
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			return fmt.Errorf("duplicate download output %q (possibly after decompression)", name)
		}
		for parent := filepath.Dir(name); parent != "."; parent = filepath.Dir(parent) {
			if seen[parent] {
				return fmt.Errorf("download output %q collides with file %q", name, parent)
			}
		}
		seen[name] = true
	}
	return nil
}

func downloadObjects(ctx context.Context, client s3Downloads, root *os.Root, objects []downloadObject, maxBytes int64) error {
	if err := validateOutputs(objects); err != nil {
		return err
	}
	for _, obj := range objects {
		resp, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(obj.bucket), Key: aws.String(obj.key)})
		if err != nil {
			return fmt.Errorf("downloading s3://%s/%s: %w", obj.bucket, obj.key, err)
		}
		_, n, err := writeDownloadFile(root, root.Name(), obj.output, resp.Body, maxBytes, obj.compressed)
		closeErr := resp.Body.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Printf("  %s (%s)\n", obj.output, formatBytes(n))
	}
	return nil
}

func rejectLegacyCorpus(datasetDir string) error {
	entries, err := os.ReadDir(datasetDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if isCorpusFile(entry.Name()) {
			return fmt.Errorf("legacy corpus %s: move corpus files under %s/data/ before loading or pulling", filepath.Join(datasetDir, entry.Name()), datasetDir)
		}
	}
	return nil
}
func isCorpusFile(name string) bool {
	return strings.HasSuffix(name, ".csv") || strings.HasSuffix(name, ".parquet") || isCsvGzPath(name)
}

func validatePackageLayout(root *os.Root) error {
	return fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in package: %s", path)
		}
		if entry.IsDir() && (path == "data" || path == "queries" || path == "k6" || path == "fixtures") {
			return fs.SkipDir
		}
		if !entry.IsDir() && isCorpusFile(path) {
			return fmt.Errorf("corpus %s must be under data/; migrate the package before pulling", path)
		}
		return nil
	})
}

func rejectSymlink(root *os.Root, path string) error {
	info, err := root.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("destination %s is a symlink", path)
	}
	return nil
}

// prepareDestDir accepts only absent or empty real directories. Config package
// pulls and referenced asset publication must never merge with user files.
func prepareDestDir(root *os.Root, path string) error {
	if err := rejectSymlink(root, path); err != nil {
		return err
	}
	dir, err := root.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(1)
	if err != nil && err != io.EOF {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("destination %s is not empty; refusing to overwrite existing assets", path)
	}
	return nil
}

func newDownloadStage(parent *os.Root) (string, *os.Root, error) {
	name := ".pull-" + rand.Text()
	if err := parent.Mkdir(name, 0700); err != nil {
		return "", nil, err
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		_ = parent.Remove(name)
	}
	return name, root, err
}

// Open each path component relative to an already-open directory. Symlinked
// ancestors are rejected, and all subsequent writes stay anchored to os.Root.
func openDownloadParent(path string) (*os.Root, error) {
	start := "."
	if filepath.IsAbs(path) {
		start = string(filepath.Separator)
	}
	root, err := os.OpenRoot(start)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			root.Close()
			return nil, fmt.Errorf("unsafe dataset parent %q", path)
		}
		if err := rejectSymlink(root, part); err != nil {
			root.Close()
			return nil, err
		}
		if err := root.Mkdir(part, 0750); err != nil && !os.IsExist(err) {
			root.Close()
			return nil, err
		}
		next, err := root.OpenRoot(part)
		root.Close()
		if err != nil {
			return nil, err
		}
		root = next
	}
	return root, nil
}

func pullDataset(ctx context.Context, client s3Downloads, datasetsDir, name, source string, maxBytes int64, excludes []string) error {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		return fmt.Errorf("invalid dataset name %q", name)
	}
	if err := validateRelativePath(name, false); err != nil {
		return err
	}
	if maxBytes <= 0 {
		return fmt.Errorf("decompression limit must be positive")
	}
	if source == "" && len(excludes) > 0 {
		return fmt.Errorf("--exclude applies only to config-prefix objects and requires --source")
	}
	parent, err := openDownloadParent(datasetsDir)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := rejectSymlink(parent, name); err != nil {
		return err
	}
	if source == "" {
		dataset, err := parent.OpenRoot(name)
		if err != nil {
			return fmt.Errorf("data-only pull requires an existing dataset directory: %w", err)
		}
		defer dataset.Close()
		return pullReferencedAssets(ctx, client, dataset, source, maxBytes, true)
	}
	bucket, key, err := parseS3URL(source)
	if err != nil {
		return err
	}
	if isTarGzKey(key) && len(excludes) > 0 {
		return fmt.Errorf("--exclude is only supported for S3 prefix pulls")
	}
	if err := prepareDestDir(parent, name); err != nil {
		return err
	}
	stageName, stage, err := newDownloadStage(parent)
	if err != nil {
		return err
	}
	defer stage.Close()
	defer cleanupDownloadStage(parent, stageName)
	if isTarGzKey(key) {
		if err := pullTarGz(ctx, client, bucket, key, stage, maxBytes); err != nil {
			return err
		}
	} else {
		objects, err := expandReference(ctx, client, downloadReference{Source: source, Kind: "prefix", Destination: "."}, "", excludes)
		if err != nil {
			return err
		}
		if err := downloadObjects(ctx, client, stage, objects, maxBytes); err != nil {
			return err
		}
	}
	if err := pullReferencedAssets(ctx, client, stage, source, maxBytes, false); err != nil {
		return err
	}
	if err := prepareDestDir(parent, name); err != nil {
		return err
	}
	return parent.Rename(stageName, name)
}

func pullReferencedAssets(ctx context.Context, client s3Downloads, dataset *os.Root, source string, maxBytes int64, required bool) error {
	if err := validatePackageLayout(dataset); err != nil {
		return err
	}
	manifest, err := readDownloadManifest(dataset, required)
	if err != nil {
		return err
	}
	var objects []downloadObject
	if manifest != nil {
		// Validate the corpus destination before issuing referenced S3 requests.
		if err := prepareDestDir(dataset, "data"); err != nil {
			return fmt.Errorf("embedded corpus and remote data declarations are ambiguous: %w", err)
		}
		for _, ref := range manifest.Data {
			expanded, err := expandReference(ctx, client, ref, "data", nil)
			if err != nil {
				return err
			}
			objects = append(objects, expanded...)
		}
	}
	if err := validateOutputs(objects); err != nil {
		return err
	}
	stageName, stage, err := newDownloadStage(dataset)
	if err != nil {
		return err
	}
	defer stage.Close()
	defer cleanupDownloadStage(dataset, stageName)
	if err := stageProvenance(dataset, stage, source, manifest); err != nil {
		return err
	}
	if err := downloadObjects(ctx, client, stage, objects, maxBytes); err != nil {
		return err
	}
	// Publish the completed corpus, then provenance. Roll back the corpus if
	// provenance publication fails, preserving existing user files.
	if manifest != nil {
		if err := prepareDestDir(dataset, "data"); err != nil {
			return err
		}
		if err := dataset.Rename(filepath.Join(stageName, "data"), "data"); err != nil {
			return err
		}
	}
	if err := dataset.Rename(filepath.Join(stageName, "dataset.yaml"), "dataset.yaml"); err != nil {
		if manifest != nil {
			_ = dataset.Rename("data", filepath.Join(stageName, "data"))
		}
		return err
	}
	return nil
}

func stageProvenance(dataset, stage *os.Root, source string, manifest *downloadManifest) error {
	if err := rejectSymlink(dataset, "dataset.yaml"); err != nil {
		return err
	}
	provenance := map[string]any{}
	previous, err := dataset.ReadFile("dataset.yaml")
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(previous) > 0 {
		if err := yaml.Unmarshal(previous, &provenance); err != nil {
			return fmt.Errorf("reading dataset.yaml provenance: %w", err)
		}
	}
	if provenance == nil {
		provenance = map[string]any{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if source != "" {
		provenance["s3"] = source
		provenance["pulled_at"] = now
	} else if _, ok := provenance["pulled_at"]; !ok {
		provenance["pulled_at"] = now
	}
	if manifest != nil {
		if len(manifest.Data) > 0 {
			provenance["data"] = manifest.Data
		}
		provenance["assets_pulled_at"] = now
	}
	data, err := yaml.Marshal(provenance)
	if err != nil {
		return err
	}
	f, err := stage.OpenFile("dataset.yaml", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func cleanupDownloadStage(parent *os.Root, name string) {
	if err := parent.RemoveAll(name); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not remove staging directory %s: %v\n", name, err)
	}
}
