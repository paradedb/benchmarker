# Shared dataset config packages

The shared corpus lives at `s3://paradedb-benchmark/datasets/`. The separate
`benchmarker/` prefix holds schema, backend setup, k6 scripts, small generated
fixtures, and `download.yaml`. No corpus or shared query Parquet is copied into
these config packages.

The four checked-in vector datasets keep their manifests beside `schema.yaml`.
This directory holds manifests for the S3-only packages; their configuration
stays in S3. `prepare.py` assembles all nine packages in a new local directory
for review. It fetches configuration from the preserved `old/` copies and
applies tracked repository configuration and these manifests. Untracked local
work is excluded. It never uploads packages or executes downloaded scripts.

| Package              | Shared corpus below `datasets/`                           | Local data destination |
| -------------------- | --------------------------------------------------------- | ---------------------- |
| `cohere-1m`          | `cohere/v1/sampled/1m/parquet/cohere_wiki/`               | `data/` shards         |
| `cohere-10m`         | `cohere/v1/sampled/10m/parquet/cohere_wiki/`              | `data/` shards         |
| `sift-10m`           | `sift/v1/sampled/10m/parquet/sift/`                       | `data/` shards         |
| `sift-100m`          | `sift/v1/sampled/100m/parquet/sift/`                      | `data/` shards         |
| `stackoverflow-100k` | `stackoverflow/v1/sampled/100k/parquet/<table>/`          | `data/<table>/` shards |
| `stackoverflow-1m`   | `stackoverflow/v1/sampled/1m/parquet/<table>/`            | `data/<table>/` shards |
| `stackexchange`      | `stackexchange/v1/sampled/150m/csv/documents/data.csv.gz` | `data/data.csv`        |
| `wikipedia`          | `wikipedia/v1/sampled/5m/csv/documents/data.csv.gz`       | `data/data.csv`        |
| `hn_cutdown`         | `hackernews/text/v1/sampled/1m/csv/data.csv`              | `data/data.csv`        |

Stack Overflow preserves its database table names: `stackoverflow_posts`,
`comments`, `users`, and `badges`. Each prefix is explicitly mapped to the
matching local table directory. `users.age` is stored as text upstream and
converted to integer by the schema-driven reader.

`hn_cutdown` uses only the 1M text corpus (`id,text`), loaded into `hn_items`.
It is not the older full-text HN archive or the 28M vector corpus. Those remain
separate under their existing locations, including `old/`.

## Prepare and inspect

Use a Python environment with `boto3` and `PyYAML`, and build the new loader:

```bash
make loader
python3 dataset-packages/prepare.py --profile ci --output /tmp/benchmarker-packages
```

The output directory must not exist. Stage newly added repository config files
in Git before preparing: the script copies tracked paths from the working tree.
Inspect the output before uploading it. Existing source buckets and `old/`
copies must remain until their consumers have moved.

For Cohere 10M, preparation starts with `cohere-10m-new`, preserving additional
VectorChord, pg_vectorsearch, and Turbopuffer configuration and recall scripts.
Tracked repo files take precedence for overlapping paths. The newer Compose
services are added to the repo Compose configuration, preserving existing
services/settings. Legacy `vectorsearch` and `pgvector` backend directory names
become `pg_vectorsearch` and `postgres`; SQL extension names stay unchanged.
The manifest selects the shared CI corpus instead of either old corpus copy.

Manifests download only corpus into `data/`; `queries` is not a supported field.
Existing benchmark-specific JSON may remain in the config package. Query inputs
and ground truth are managed separately. The Cohere/SIFT READMEs explain how to
fetch those inputs manually and regenerate fixtures when needed. Pulling a
manifest never fetches query assets or executes generation scripts.

## Publish and pull

After inspecting the prepared packages and validating the implementation:

```bash
AWS_PROFILE=ci aws s3 sync /tmp/benchmarker-packages/ \
  s3://paradedb-benchmark/benchmarker/
```

This command does not delete source objects. For a staged rollout, first use a
new review prefix and verify its packages before updating canonical config keys.
A fresh package pull uses the source explicitly; local names remain your choice:

```bash
AWS_PROFILE=ci ./bin/loader pull --dataset stackoverflow \
  --source s3://paradedb-benchmark/benchmarker/stackoverflow-1m/
```

The destination must be absent or empty. Checked-in vector configs instead use
`AWS_PROFILE=ci ./bin/loader pull --dataset cohere-1m` without `--source`.
For Stack Exchange, select a decompression limit large enough for the 150M-row
CSV; the default limit is 100 GiB.

## Verification

```bash
go test ./...
AWS_PROFILE=ci BENCHMARKER_S3_SMOKE=1 go test ./cmd/loader -run TestS3Smoke -v
```

The opt-in smoke test downloads about 0.16 MB: one real Stack Overflow CSV
object and a small Stack Overflow corpus Parquet, then reads both through the schema-driven
reader. Normal tests use fake S3 responses. After preparing packages, validate both
pull flows and every schema/table mapping with:

```bash
BENCHMARKER_PREPARED_PACKAGES=/tmp/benchmarker-packages \
  go test ./cmd/loader -run TestPreparedPackages -v
```

After staging the packages in S3, verify fresh pulls using authenticated config
downloads, real corpus listings, and tiny synthetic corpus payloads:

```bash
AWS_PROFILE=ci BENCHMARKER_S3_PACKAGES=s3://paradedb-benchmark/benchmarker/ \
  go test ./cmd/loader -run TestS3PackageSmoke -v
```

The test adapter replaces corpus object bodies to avoid transferring large
datasets; this validates download orchestration, not full-corpus load/recall.
Corpus/schema compatibility can be
checked using S3 range reads of Parquet metadata and representative row groups,
plus CSV header ranges, without downloading whole corpora. For a package rollout,
verify each fresh package with small representative objects in a test S3 client;
reserve full corpus pulls for actual benchmarks.

Snapshot migration and ParadeDB CI wiring are separate work.
