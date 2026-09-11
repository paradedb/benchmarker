import db from "k6/x/database";

// Cohere 10M vector benchmark: ParadeDB (pg_search vector index) vs
// Elasticsearch dense_vector kNN, all containers capped at the same 8g —
// the data-larger-than-memory regime (neither index fits in cache).
// Unfiltered and 1%-filtered (performance-section) shapes.

const backends = db.backends({
  datasetPath: "../",
  backends: [
    {
      type: "paradedb",
      alias: "paradedb",
      connection:
        __ENV.PARADEDB_URL ||
        "postgres://postgres:postgres@localhost:5432/benchmark",
      container: __ENV.PARADEDB_CONTAINER || "paradedb",
      color: "blue",
    },
    {
      // Filtered scenario needs its own probe operating point; GUCs are
      // per-connection, so it rides a second connection string.
      type: "paradedb",
      alias: "paradedb_filtered",
      connection:
        __ENV.PARADEDB_FILTERED_URL ||
        `postgres://postgres:postgres@localhost:5432/benchmark?options=-c%20paradedb.vector_cluster_max_probe%3D${__ENV.PDB_FILTERED_PROBE || "0.05"}`,
      container: __ENV.PARADEDB_CONTAINER || "paradedb",
      color: "purple",
    },
    {
      type: "elasticsearch",
      alias: "elasticsearch",
      connection: __ENV.ELASTICSEARCH_URL || "http://localhost:9200",
      container: __ENV.ELASTICSEARCH_CONTAINER || "elasticsearch",
      color: "green",
    },
  ],
});

const vectors = db.terms(JSON.parse(open("./query_vectors.json")));

const timer = db.timer({ duration: "60s", gap: "10s" });

// Closed-model VU count; latency claims use 1 (vu1 closed), raise for
// throughput probing (-e VUS=5).
const VUS = Number(__ENV.VUS || 1);

const scenarios = {
  paradedb_knn: {
    executor: "constant-vus",
    vus: VUS,
    duration: timer.duration(),
    startTime: timer.get(),
    exec: "paradedbKnn",
  },
  elasticsearch_knn: {
    executor: "constant-vus",
    vus: VUS,
    duration: timer.duration(),
    startTime: timer.advanceAndGet(),
    exec: "elasticsearchKnn",
  },
  paradedb_knn_filtered: {
    executor: "constant-vus",
    vus: VUS,
    duration: timer.duration(),
    startTime: timer.advanceAndGet(),
    exec: "paradedbKnnFiltered",
    tags: { chart: "knn_filtered" },
  },
  elasticsearch_knn_filtered: {
    executor: "constant-vus",
    vus: VUS,
    duration: timer.duration(),
    startTime: timer.advanceAndGet(),
    exec: "elasticsearchKnnFiltered",
    tags: { chart: "knn_filtered" },
  },
};

export const collectMetrics = backends.addDockerMetricsCollector(
  scenarios,
  "300s",
);

export const options = { scenarios };

const PARADEDB_KNN = `
  SELECT _id, title
  FROM cohere_wiki
  WHERE _id @@@ paradedb.all()
  ORDER BY emb <=> $1::vector(1024)
  LIMIT 10
`;

export function paradedbKnn() {
  backends.get("paradedb").query(PARADEDB_KNN, vectors.next());
}

// Measured ~96% recall@10 operating point on the as-loaded multi-segment
// index (0.961), matched to paradedb's max_probe=0.035 (0.959). See README.
const ES_NUM_CANDIDATES = Number(__ENV.ES_NUM_CANDIDATES || 95);

// Filtered kNN: the paradedb.com performance-section shape — a ~1%-selective
// full-text filter ('battle') gating the vector search. Mirrors upstream
// knn_top10_1pct.sql; ground truth is the 1pct variant.
const FILTER_TERM = __ENV.FILTER_TERM || "battle";

const PARADEDB_KNN_FILTERED = `
  SELECT _id, title
  FROM cohere_wiki
  WHERE text ||| '${FILTER_TERM}'
  ORDER BY emb <=> $1::vector(1024)
  LIMIT 10
`;

export function paradedbKnnFiltered() {
  backends.get("paradedb_filtered").query(PARADEDB_KNN_FILTERED, vectors.next());
}

// Both engines saturate at recall 1.000 on the 1%-filtered shape (the
// filter leaves ~10k eligible docs); these are the cheapest 1.000 points.
const ES_FILTERED_CANDIDATES = Number(__ENV.ES_FILTERED_CANDIDATES || 40);

export function elasticsearchKnnFiltered() {
  backends.get("elasticsearch").query(
    JSON.stringify({
      knn: {
        field: "emb",
        query_vector: JSON.parse(vectors.next()),
        k: 10,
        num_candidates: ES_FILTERED_CANDIDATES,
        filter: { match: { text: FILTER_TERM } },
      },
      size: 10,
      _source: false,
      fields: ["title"],
    }),
    "cohere_wiki",
  );
}

export function elasticsearchKnn() {
  backends.get("elasticsearch").query(
    JSON.stringify({
      knn: {
        field: "emb",
        query_vector: JSON.parse(vectors.next()),
        k: 10,
        num_candidates: ES_NUM_CANDIDATES,
      },
      size: 10,
      _source: false,
      fields: ["title"],
    }),
    "cohere_wiki",
  );
}
