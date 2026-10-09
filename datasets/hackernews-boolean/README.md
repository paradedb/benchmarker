# Hacker News Boolean workloads

A frozen corpus of nested text and scalar predicates, run as `count`, `bm25`
(score descending), or `time` (time descending). Pure filter cases omit BM25.
The suite uses the existing `hn_items` fixture and never reloads or replaces it
during calibration, validation, or measurement.

## Prepare the database

Point `PARADEDB_URL` at the HN database. The repository root is the working
directory for the commands below. Requirements: Python 3, Node.js, `psql`, and
the current benchmarker binary (`make k6`).

The index must cover `id`, `title`, `text`, `time`, `score`, `type`, `by`,
`parent`, `descendants`, `dead`, and `deleted`. Inspect existing indexes first;
only run the index script on a fixture without an existing ParadeDB index:

```sh
psql "$PARADEDB_URL" -X -c '\d hn_items'
psql "$PARADEDB_URL" -X -f datasets/hackernews-boolean/paradedb/post.sql
```

For a new database, `schema.yaml` and `paradedb/pre.sql` describe the existing
15-column HN CSV. Link your `data.csv` into this directory and use the loader.
The dataset itself and generated artifacts are excluded from Git.

## Freeze a corpus

```sh
python3 datasets/hackernews-boolean/calibrate.py
node datasets/hackernews-boolean/k6/generate.mjs \
  datasets/hackernews-boolean/artifacts/catalog.json \
  datasets/hackernews-boolean/artifacts/queries.json
python3 datasets/hackernews-boolean/validate.py \
  datasets/hackernews-boolean/artifacts/queries.json
```

Calibration measures exact index counts for text disjunctions and samples
0.1% of heap pages for scalar distributions. Filter thresholds use empirical
quantiles; categorical filters use observed values. A leaf's `measurement`
identifies exact versus sampled frequencies. Null rates and distinct counts
are **sample** statistics, not full-column guarantees. This is a page sample,
so clustered data can bias it. Full predicate counts are saved by validation.
Zero/all-match scalar predicates and zero-match text leaves are excluded.

Generation defaults to seed `20261008` and 12 clauses (optional third/fourth
command arguments after the script: seed and clause count). It produces:

- Six shapes: AND, OR, AND of OR groups, OR of AND groups, a selective gate
  followed by a union, and a union excluding two predicates. Every text leaf
  uses `|||` (single terms or multiword disjunctions); there are no phrases.
- Three mixes: eight text/four filters, six text/six filters, and filters only.
- Four relative density profiles: broad, sparse, alternating broad/sparse, and
  one sparse leaf among broad leaves. These select from the measured tails;
  they do not promise a fixed percentage or a physical posting bitmap.

The default corpus has 72 predicates and 192 requests. Query IDs include a
corpus fingerprint, and measured top-k IDs also include the requested limit.
The same mix/profile
uses the same leaf set across all six shapes. Leaves are distinct,
but constraints on the same field may overlap, contradict, or be simplified
by the planner. Preserve those observations in the validation artifact rather
than treating every generated tree as 12 independently executed conditions.
`queries.json` contains the tree, leaf frequencies, stable ID, SQL, and parameters.

Validation executes every request, saves plans/results and complete predicate
counts, and rejects heap scans, residual filters, and external sorts. Every
request has a `pdb.all()` anchor, including pure filters. A custom scan alone
does not prove bitmap use: inspect its query and collector details. Equality
filters may use postings instead of a fast-field scan. The scalar bitmap
prototype currently requires full single-value columns and eligible unscored
collection. Nullable/multivalued columns exercise fallbacks.

Requests proven empty by PostgreSQL before reaching the index are labeled
`planner_empty` controls. On successful validation, `validated-queries.json`
contains only the modes that execute inside ParadeDB; this is the runner's
default manifest. Empty results that still execute inside ParadeDB remain.
Freeze this validated manifest across revisions too, so differences in planning
do not silently change the measured query set.

## Run

First run one complete pass of each mode as warmup, writing to a separate
output directory. Then use at least three measured passes:

```sh
for mode in count bm25 time; do
  PARADEDB_URL="$PARADEDB_URL" MODE="$mode" PASSES=3 \
    WARMUP_SECONDS=0 DURATION_SECONDS=1800 VUS=1 \
    DASHBOARD_EXPORT_DIR="datasets/hackernews-boolean/artifacts/$mode" \
    ./k6 run --out dashboard=html,query_csv \
      datasets/hackernews-boolean/k6/search.js
done
```

`PASSES` uses shared iterations to visit every selected query equally often.
`DURATION_SECONDS` is its safety limit; check coverage after the run because
reaching that limit can leave requests unvisited. For a timed throughput run,
omit `PASSES`: defaults are 30 seconds of excluded warmup and 300 measured
seconds. Set `VUS=8` after establishing single-client results. `TELEMETRY=1`
enables the benchmarker's resource/performance collector; it can extend a
fixed-pass run to its duration limit, so leave it off for smoke tests.

Other options:

| Option                    | Default                               | Meaning                                   |
| ------------------------- | ------------------------------------- | ----------------------------------------- |
| `MODE`                    | `count`                               | `count`, `bm25`, or `time`                |
| `TOP_K`                   | `10`                                  | Limit for the two ordering modes          |
| `QUERIES`                 | `../artifacts/validated-queries.json` | Manifest path, relative to `k6/search.js` |
| `SEED`                    | `20261008`                            | Reproducible execution order              |
| `SHAPE`, `MIX`, `PROFILE` | all                                   | Run a slice of the corpus                 |
| `QUERY_TIMEOUT_SECONDS`   | `30`                                  | Per-query timeout; errors fail the run    |
| `PARADEDB_CONTAINER`      | empty                                 | Optional Docker telemetry target          |

Column ordering does not compute BM25. The two top-k modes return only IDs and
their ordering value to reduce projection costs. They intentionally do not add
an ID tiebreaker, which can alter sort pushdown. Equal-score/time boundary ties
may return different IDs; comparing such results requires tie-aware validation.

## Compare and verify coverage

```sh
python3 datasets/hackernews-boolean/report.py \
  datasets/hackernews-boolean/artifacts/validated-queries.json candidate_queries.csv \
  --mode count --baseline baseline_queries.csv --min-samples 3
```

Pass matching `--shape`, `--mix`, or `--profile` when analyzing a sliced run.
Pass `--top-k` if you changed `TOP_K`.
The report rejects missing/undersampled queries and lists individual P50
changes. Three observations are a smoke-test floor, not enough for tail-latency
claims. Use many more passes for P95/P99; the CSV percentiles are estimates.

Reuse the **same frozen manifest** for both revisions. Keep dataset, index
layout, visibility, PostgreSQL settings, and projection fixed. Compare the
posting-bitmap branch with the fast-field add-on while leaving posting bitmaps
enabled in both. Run versions separately on a shared host; repeat in reverse
order before interpreting small differences. Calibration and validation set
parallelism to zero in their own connections; k6 uses the server/connection
settings, which must be made identical for both versions.

## Generator checks

```sh
node --test datasets/hackernews-boolean/k6/queries.test.js
python3 -m unittest discover -s datasets/hackernews-boolean -p 'test_*.py'
```
