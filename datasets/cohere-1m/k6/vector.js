import db from "k6/x/database";

// 1m slice of the Cohere vector benchmark: ParadeDB (pg_search vector index)
// vs PostgreSQL + pgvector HNSW, all containers capped at the same 8g so
// every index fits fully in cache — the complement to cohere-10m's
// data-larger-than-memory regime.
//
// Loads live in the benchmark_1m database, keeping cohere-10m's tables in
// `benchmark` intact on the same containers.

const backends = db.backends({
  datasetPath: "../",
  backends: [
    {
      type: "paradedb",
      alias: "paradedb",
      connection:
        __ENV.PARADEDB_URL ||
        "postgres://postgres:postgres@localhost:5432/benchmark_1m",
      container: __ENV.PARADEDB_CONTAINER || "paradedb",
      color: "blue",
    },
    {
      type: "postgres",
      alias: "postgres",
      connection:
        __ENV.POSTGRES_URL ||
        "postgres://postgres:postgres@localhost:5433/benchmark_1m",
      container: __ENV.POSTGRES_CONTAINER || "postgres",
      color: "orange",
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

const scenarios = {
  paradedb_knn: {
    executor: "constant-vus",
    vus: 5,
    duration: timer.duration(),
    startTime: timer.get(),
    exec: "paradedbKnn",
  },
  postgres_knn: {
    executor: "constant-vus",
    vus: 5,
    duration: timer.duration(),
    startTime: timer.advanceAndGet(),
    exec: "postgresKnn",
  },
  elasticsearch_knn: {
    executor: "constant-vus",
    vus: 5,
    duration: timer.duration(),
    startTime: timer.advanceAndGet(),
    exec: "elasticsearchKnn",
  },
};

export const collectMetrics = backends.addDockerMetricsCollector(
  scenarios,
  "220s",
);

export const options = { scenarios };

const PARADEDB_KNN = `
  SELECT _id, title
  FROM cohere_wiki
  WHERE _id @@@ paradedb.all()
  ORDER BY emb <=> $1::vector(1024)
  LIMIT 10
`;

const POSTGRES_KNN = `
  SELECT _id, title
  FROM cohere_wiki
  ORDER BY emb <=> $1::vector(1024)
  LIMIT 10
`;

export function paradedbKnn() {
  backends.get("paradedb").query(PARADEDB_KNN, vectors.next());
}

export function postgresKnn() {
  backends.get("postgres").query(POSTGRES_KNN, vectors.next());
}

// Tune to the same measured recall@10 as the SQL backends (measure_recall.py).
const ES_NUM_CANDIDATES = Number(__ENV.ES_NUM_CANDIDATES || 150);

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
