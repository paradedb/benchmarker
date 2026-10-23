package main

import (
	"bytes"
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/paradedb/benchmarker/drivers"
)

// Opt-in authenticated smoke test, limited to ~0.55 MB of real CSV/Parquet.
// AWS_PROFILE=ci BENCHMARKER_S3_SMOKE=1 go test ./cmd/loader -run TestS3Smoke -v
func TestS3Smoke(t *testing.T) {
	if os.Getenv("BENCHMARKER_S3_SMOKE") != "1" {
		t.Skip("set BENCHMARKER_S3_SMOKE=1 to use authenticated S3")
	}
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dir := pullTestDir(t)
	manifest := `version: 1
data:
  - source: s3://paradedb-benchmark/datasets/stackoverflow/v1/sampled/10m/csv/users/data_1.csv
    kind: object
    destination: users.csv
  - source: s3://paradedb-benchmark/datasets/cohere/v1/queries/cohere_queries.parquet
    kind: object
    destination: vectors.parquet
`
	putTestFile(t, dir, "smoke/download.yaml", []byte(manifest))
	if err := pullDataset(ctx, s3.NewFromConfig(cfg), dir, "smoke", "", 1<<20, nil); err != nil {
		t.Fatal(err)
	}
	for filename, schema := range map[string]*drivers.Schema{
		"users.csv":       {Columns: map[string]string{"id": "bigint", "display_name": "text"}},
		"vectors.parquet": {Columns: map[string]string{"emb": "vector(1024)"}},
	} {
		source, err := drivers.OpenRowSource(filepath.Join(dir, "smoke/data", filename), schema)
		if err != nil {
			t.Fatal(err)
		}
		_, err = source.Next()
		source.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

// packageSmokeClient exercises authenticated config/query downloads and real S3
// listings, substituting tiny corpus payloads so a package smoke test cannot
// download hundreds of GB. This adapter is used only by this opt-in test.
type packageSmokeClient struct {
	*s3.Client
	csvGzip []byte
}

func (c packageSmokeClient) GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := aws.ToString(in.Key)
	if strings.HasPrefix(key, "datasets/") && strings.Contains(key, "/sampled/") {
		data := []byte("id,body,text\n1,example,example\n")
		if strings.HasSuffix(key, ".parquet") {
			data = []byte("PAR1")
		}
		if isCsvGzPath(key) {
			data = c.csvGzip
		}
		return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data))}, nil
	}
	return c.Client.GetObject(ctx, in, opts...)
}

func TestS3PackageSmoke(t *testing.T) {
	source := os.Getenv("BENCHMARKER_S3_PACKAGES")
	if source == "" {
		t.Skip("set BENCHMARKER_S3_PACKAGES to an S3 config package prefix")
	}
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client := packageSmokeClient{Client: s3.NewFromConfig(cfg), csvGzip: testGzip(t, []byte("id,body,text\n1,example,example\n"))}
	for _, name := range []string{"cohere-1m", "cohere-10m", "sift-10m", "sift-100m", "stackoverflow-100k", "stackoverflow-1m", "stackexchange", "wikipedia", "hn_cutdown"} {
		t.Run(name, func(t *testing.T) {
			dir := pullTestDir(t)
			if err := pullDataset(ctx, client, dir, name, strings.TrimSuffix(source, "/")+"/"+name+"/", 1<<20, nil); err != nil {
				t.Fatal(err)
			}
			dataset := filepath.Join(dir, name)
			schema, err := loadSchema(dataset)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := resolveTableData(dataset, schema); err != nil {
				t.Fatal(err)
			}
			assertNoStages(t, dir)
		})
	}
}
