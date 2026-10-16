package loader

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/paradedb/benchmarker/backends"
)

// loadTyped reuses the CLI loader so IDs, vectors, booleans, and nulls have the
// same representation during setup as they do in streamed k6 workloads.
func (l *Loader) loadTyped(cli *backends.CLILoader, config map[string]interface{}, path, dataset string, batchSize int) map[string]interface{} {
	defer cli.Close()
	failure := func(err error) map[string]interface{} { return map[string]interface{}{"error": err.Error()} }
	if _, ok := config["columns"]; ok {
		return failure(fmt.Errorf("load: schema and columns cannot be combined; select columns in the schema"))
	}
	stream, err := l.OpenDocumentStream(path, config)
	if err != nil {
		return failure(err)
	}
	defer stream.Close()
	schema := *stream.schema
	if table, ok := config["table"].(string); ok && table != "" {
		schema.Table = table
	}
	dataset = l.resolvePath(dataset)
	if err := cli.ValidateSchema(&schema); err != nil {
		return failure(err)
	}
	ctx := context.Background()
	if l.vu != nil && l.vu.Context() != nil {
		ctx = l.vu.Context()
	}
	start := time.Now()
	if dataset != "" {
		if err := cli.RunPre(ctx, filepath.Join(dataset, cli.Name()), &schema); err != nil {
			return failure(fmt.Errorf("pre failed: %w", err))
		}
	}
	n, err := cli.Load(ctx, &schema, stream.path, batchSize, 1)
	if err != nil {
		return failure(fmt.Errorf("load failed: %w", err))
	}
	loadTime := time.Since(start)
	var indexTime time.Duration
	if dataset != "" {
		indexStart := time.Now()
		if err := cli.RunPost(ctx, filepath.Join(dataset, cli.Name()), &schema); err != nil {
			return failure(fmt.Errorf("post failed: %w", err))
		}
		indexTime = time.Since(indexStart)
	}
	return map[string]interface{}{"loaded": n, "loadTimeMs": loadTime.Milliseconds(), "indexTimeMs": indexTime.Milliseconds(), "totalTimeMs": time.Since(start).Milliseconds()}
}
