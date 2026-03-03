// Loader CLI for bulk loading data into search backends.
//
// Usage:
//
//	loader load ./datasets/wikipedia                    # Load all backends
//	loader load --backend paradedb ./datasets/wikipedia # Load specific backend
//	loader drop --backend paradedb ./datasets/wikipedia # Drop tables/indexes
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	_ "github.com/paradedb/benchmarks" // triggers backend init() via backends.go imports
	"github.com/paradedb/benchmarks/backends"
	"gopkg.in/yaml.v3"
)

func main() {
	loadCmd := flag.NewFlagSet("load", flag.ContinueOnError)
	loadBackend := loadCmd.String("backend", "", "Specific backend ("+strings.Join(backends.RegisteredBackends(), ", ")+")")
	loadBatchSize := loadCmd.Int("batch-size", 10000, "Batch size for bulk loading")
	loadWorkers := loadCmd.Int("workers", 1, "Number of parallel workers")

	dropCmd := flag.NewFlagSet("drop", flag.ContinueOnError)
	dropBackend := dropCmd.String("backend", "", "Specific backend to drop")

	pullCmd := flag.NewFlagSet("pull", flag.ContinueOnError)
	pullDataset := pullCmd.String("dataset", "", "Dataset name (creates ./datasets/<name>/)")
	pullSource := pullCmd.String("source", "", "S3 source URL (s3://bucket/prefix/)")
	pullAnonymous := pullCmd.Bool("anonymous", false, "Use anonymous access for public buckets")

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "help", "-h", "--help":
		printUsage()
		os.Exit(0)

	case "load":
		if err := loadCmd.Parse(os.Args[2:]); err != nil {
			if err == flag.ErrHelp {
				os.Exit(0)
			}
			os.Exit(1)
		}
		if loadCmd.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "Error: dataset directory required")
			fmt.Fprintln(os.Stderr, "Usage: loader load [--backend <name>] <dataset-dir>")
			os.Exit(1)
		}
		if loadCmd.NArg() > 1 {
			fmt.Fprintf(os.Stderr, "Error: unexpected argument: %s\n", loadCmd.Arg(1))
			fmt.Fprintln(os.Stderr, "Usage: loader load [--backend <name>] <dataset-dir>")
			os.Exit(1)
		}
		runLoad(loadCmd.Arg(0), *loadBackend, *loadBatchSize, *loadWorkers)

	case "drop":
		if err := dropCmd.Parse(os.Args[2:]); err != nil {
			if err == flag.ErrHelp {
				os.Exit(0)
			}
			os.Exit(1)
		}
		if dropCmd.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "Error: dataset directory required")
			fmt.Fprintln(os.Stderr, "Usage: loader drop [--backend <name>] <dataset-dir>")
			os.Exit(1)
		}
		if dropCmd.NArg() > 1 {
			fmt.Fprintf(os.Stderr, "Error: unexpected argument: %s\n", dropCmd.Arg(1))
			fmt.Fprintln(os.Stderr, "Usage: loader drop [--backend <name>] <dataset-dir>")
			os.Exit(1)
		}
		runDrop(dropCmd.Arg(0), *dropBackend)

	case "pull":
		if err := pullCmd.Parse(os.Args[2:]); err != nil {
			if err == flag.ErrHelp {
				os.Exit(0)
			}
			os.Exit(1)
		}
		if pullCmd.NArg() > 0 {
			fmt.Fprintf(os.Stderr, "Error: unexpected argument: %s\n", pullCmd.Arg(0))
			fmt.Fprintln(os.Stderr, "Usage: loader pull --dataset <name> --source s3://bucket/prefix/")
			os.Exit(1)
		}
		if *pullDataset == "" || *pullSource == "" {
			fmt.Fprintln(os.Stderr, "Error: --dataset and --source are required")
			fmt.Fprintln(os.Stderr, "Usage: loader pull --dataset <name> --source s3://bucket/prefix/")
			os.Exit(1)
		}
		runPull(*pullDataset, *pullSource, *pullAnonymous)

	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command: %s\n", os.Args[1])
		fmt.Fprintln(os.Stderr, "Run 'loader help' for usage")
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Loader - Bulk load data into search backends

Usage:
  loader load [--backend <name>] [--batch-size <n>] [--workers <n>] <dataset-dir>
  loader drop [--backend <name>] <dataset-dir>
  loader pull --dataset <name> --source <s3-url> [--anonymous]
  loader help

Commands:
  load    Run pre.sql/json, bulk load CSV, run post.sql/json
  drop    Drop tables/indexes for the dataset
  pull    Download dataset from S3 to ./datasets/<name>/
  help    Show this help message

Backends:
  ` + strings.Join(backends.RegisteredBackends(), ", ") + `

Options:
  --backend <name>   Load/drop specific backend (default: all backends)
  --batch-size <n>   Rows per batch (default: 10000)
  --workers <n>      Parallel workers (default: 1)
  --dataset <name>   Dataset name for pull command
  --source <url>     S3 source URL (s3://bucket/prefix/)
  --anonymous        Use anonymous access for public S3 buckets

Environment Variables:
  PARADEDB_URL       ParadeDB connection string
  POSTGRES_FTS_URL   PostgreSQL FTS connection string
  PG_TEXTSEARCH_URL  pg_textsearch connection string
  ELASTICSEARCH_URL  Elasticsearch address
  OPENSEARCH_URL     OpenSearch address
  CLICKHOUSE_URL     ClickHouse connection string
  MONGODB_URL        MongoDB connection string
  AWS_REGION         AWS region for S3 (default: us-east-1)
  AWS_PROFILE        AWS profile to use for credentials

Examples:
  loader load --backend paradedb ./datasets/sample
  loader load --backend paradedb --workers 4 ./datasets/sample
  loader load ./datasets/sample                                    # all backends
  loader drop --backend paradedb ./datasets/sample
  loader pull --dataset large --source s3://mybucket/datasets/large/
  loader pull --dataset test --source s3://fts-bench/datasets/test/ --anonymous
  PARADEDB_URL=postgres://user:pass@host:5432/db loader load --backend paradedb ./datasets/sample`)
}

// getConnection returns the connection string for a backend.
func getConnection(name string) string {
	cfg, ok := backends.GetConfig(name)
	if !ok {
		return ""
	}
	if val := os.Getenv(cfg.EnvVar); val != "" {
		return val
	}
	// Backward-compatibility for prior postgresfts env var name.
	if name == "postgresfts" {
		if val := os.Getenv("POSTGRESFTS_URL"); val != "" {
			return val
		}
	}
	return cfg.DefaultConn
}

func runLoad(datasetDir string, backendName string, batchSize int, workers int) {
	schema, err := loadSchema(datasetDir)
	if err != nil {
		fmt.Printf("Error loading schema: %v\n", err)
		os.Exit(1)
	}

	csvPath := findCSV(datasetDir)
	if csvPath == "" {
		fmt.Println("Error: no CSV file found in dataset directory")
		os.Exit(1)
	}

	var loaders []*backends.CLILoader
	if backendName != "" {
		loader := backends.GetCLILoader(backendName, getConnection(backendName))
		if loader == nil {
			fmt.Printf("Error: unknown backend '%s'\n", backendName)
			os.Exit(1)
		}
		loaders = []*backends.CLILoader{loader}
	} else {
		loaders = backends.GetAllCLILoaders(getConnection)
	}

	ctx := context.Background()
	overallFailed := false

	for _, b := range loaders {
		func(loader *backends.CLILoader) {
			defer func() {
				if err := loader.Close(); err != nil {
					fmt.Printf("Warning: failed to close %s: %v\n", loader.Name(), err)
				}
			}()

			dir := filepath.Join(datasetDir, loader.Name())
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				fmt.Printf("Skipping %s (no config directory)\n", loader.Name())
				return
			}

			fmt.Printf("\n=== %s ===\n", strings.ToUpper(loader.Name()))

			// Run pre
			fmt.Print("Running pre... ")
			start := time.Now()
			if err := loader.RunPre(ctx, dir, schema); err != nil {
				fmt.Printf("FAILED: %v\n", err)
				overallFailed = true
				return
			}
			fmt.Printf("OK (%.2fs)\n", time.Since(start).Seconds())

			// Load data
			if workers > 1 {
				fmt.Printf("Loading data (batch size: %d, workers: %d)... ", batchSize, workers)
			} else {
				fmt.Printf("Loading data (batch size: %d)... ", batchSize)
			}
			start = time.Now()
			count, err := loader.Load(ctx, schema, csvPath, batchSize, workers)
			if err != nil {
				fmt.Printf("FAILED: %v\n", err)
				overallFailed = true
				return
			}
			elapsed := time.Since(start).Seconds()
			rate := 0.0
			if elapsed > 0 {
				rate = float64(count) / elapsed
			}
			fmt.Printf("OK (%d rows, %.2fs, %.0f rows/sec)\n", count, elapsed, rate)

			// Run post
			fmt.Print("Running post... ")
			start = time.Now()
			if err := loader.RunPost(ctx, dir, schema); err != nil {
				fmt.Printf("FAILED: %v\n", err)
				overallFailed = true
				return
			}
			fmt.Printf("OK (%.2fs)\n", time.Since(start).Seconds())
		}(b)
	}

	if overallFailed {
		fmt.Println("\nCompleted with errors.")
		os.Exit(1)
	}

	fmt.Println("\nDone!")
}

func runDrop(datasetDir string, backendName string) {
	schema, err := loadSchema(datasetDir)
	if err != nil {
		fmt.Printf("Error loading schema: %v\n", err)
		os.Exit(1)
	}

	var loaders []*backends.CLILoader
	if backendName != "" {
		loader := backends.GetCLILoader(backendName, getConnection(backendName))
		if loader == nil {
			fmt.Printf("Error: unknown backend '%s'\n", backendName)
			os.Exit(1)
		}
		loaders = []*backends.CLILoader{loader}
	} else {
		loaders = backends.GetAllCLILoaders(getConnection)
	}

	ctx := context.Background()

	for _, b := range loaders {
		fmt.Printf("Dropping %s... ", b.Name())
		if err := b.Drop(ctx, schema); err != nil {
			fmt.Printf("FAILED: %v\n", err)
		} else {
			fmt.Println("OK")
		}
		b.Close()
	}
}

func loadSchema(datasetDir string) (*backends.Schema, error) {
	schemaPath := filepath.Join(datasetDir, "schema.yaml")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("reading schema.yaml: %w", err)
	}

	var schema backends.Schema
	if err := yaml.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("parsing schema.yaml: %w", err)
	}

	if schema.Table == "" {
		schema.Table = "documents"
	}

	return &schema, nil
}

func findCSV(datasetDir string) string {
	entries, err := os.ReadDir(datasetDir)
	if err != nil {
		return ""
	}

	var csvFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".csv") {
			csvFiles = append(csvFiles, e.Name())
		}
	}
	if len(csvFiles) == 0 {
		return ""
	}
	sort.Strings(csvFiles)
	return filepath.Join(datasetDir, csvFiles[0])
}

// ============================================================================
// S3 Pull
// ============================================================================

func runPull(datasetName, sourceURL string, anonymous bool) {
	bucket, prefix, err := parseS3URL(sourceURL)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	destDir := filepath.Join("datasets", datasetName)
	fmt.Printf("Pulling from s3://%s/%s to %s\n", bucket, prefix, destDir)
	if anonymous {
		fmt.Println("Using anonymous access (public bucket)")
	}

	ctx := context.Background()

	var cfg aws.Config
	if anonymous {
		cfg, err = config.LoadDefaultConfig(ctx,
			config.WithCredentialsProvider(aws.AnonymousCredentials{}),
			config.WithRegion("us-east-1"),
		)
	} else {
		cfg, err = config.LoadDefaultConfig(ctx)
	}
	if err != nil {
		fmt.Printf("Error loading AWS config: %v\n", err)
		os.Exit(1)
	}

	client := s3.NewFromConfig(cfg)

	var objects []string
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			fmt.Printf("Error listing objects: %v\n", err)
			os.Exit(1)
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if strings.HasSuffix(key, "/") {
				continue
			}
			objects = append(objects, key)
		}
	}

	if len(objects) == 0 {
		fmt.Printf("No objects found at s3://%s/%s\n", bucket, prefix)
		os.Exit(1)
	}

	fmt.Printf("Found %d files to download\n", len(objects))

	var downloaded, failed int
	var totalBytes int64

	for _, key := range objects {
		relPath, localPath, err := resolveDownloadPath(destDir, prefix, key)
		if err != nil {
			fmt.Printf("  Skipping %s: %v\n", key, err)
			failed++
			continue
		}

		if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
			fmt.Printf("  Error creating directory for %s: %v\n", relPath, err)
			failed++
			continue
		}

		resp, err := client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			fmt.Printf("  Error downloading %s: %v\n", relPath, err)
			failed++
			continue
		}

		f, err := os.Create(localPath)
		if err != nil {
			resp.Body.Close()
			fmt.Printf("  Error creating %s: %v\n", localPath, err)
			failed++
			continue
		}

		n, err := io.Copy(f, resp.Body)
		resp.Body.Close()
		f.Close()

		if err != nil {
			fmt.Printf("  Error writing %s: %v\n", localPath, err)
			failed++
			continue
		}

		totalBytes += n
		downloaded++
		fmt.Printf("  %s (%s)\n", relPath, formatBytes(n))
	}

	fmt.Printf("\nComplete: %d files downloaded (%.2f MB)", downloaded, float64(totalBytes)/1024/1024)
	if failed > 0 {
		fmt.Printf(", %d failed", failed)
	}
	fmt.Println()
	if failed > 0 {
		os.Exit(1)
	}
}

func resolveDownloadPath(destDir, prefix, key string) (string, string, error) {
	relPath := strings.TrimPrefix(key, prefix)
	if prefix == "" && strings.HasPrefix(relPath, "/") {
		return "", "", fmt.Errorf("absolute path %q is not allowed", relPath)
	}
	relPath = strings.TrimPrefix(relPath, "/")
	cleanRelPath := filepath.Clean(relPath)

	if cleanRelPath == "." || cleanRelPath == "" {
		return "", "", fmt.Errorf("empty output path")
	}
	if filepath.IsAbs(cleanRelPath) || cleanRelPath == ".." || strings.HasPrefix(cleanRelPath, ".."+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("unsafe path %q", relPath)
	}

	localPath := filepath.Join(destDir, cleanRelPath)
	destAbs, err := filepath.Abs(destDir)
	if err != nil {
		return "", "", err
	}
	localAbs, err := filepath.Abs(localPath)
	if err != nil {
		return "", "", err
	}
	if localAbs != destAbs && !strings.HasPrefix(localAbs, destAbs+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("path escapes destination directory")
	}

	return cleanRelPath, localPath, nil
}

func parseS3URL(url string) (bucket, prefix string, err error) {
	if !strings.HasPrefix(url, "s3://") {
		return "", "", fmt.Errorf("invalid S3 URL: must start with s3://")
	}

	path := strings.TrimPrefix(url, "s3://")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		return "", "", fmt.Errorf("invalid S3 URL: missing bucket name")
	}

	bucket = parts[0]
	if len(parts) > 1 {
		prefix = strings.TrimSuffix(parts[1], "/")
	}
	return bucket, prefix, nil
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
