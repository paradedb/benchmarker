import db from "k6/x/database";

const backends = db.backends({
  datasetPath: "../",
  backends: [
    "paradedb",
    "postgres",
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

// num_candidates is the ES recall knob (ef_search analog). Default is the
// measured 95% recall@10 operating point on the 10m single-segment index
// (see measure_recall.py; 40 → 0.835, 150 → 0.951, 300 → 0.966). Match the
// SQL backends to the same measured recall before comparing latency.
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
