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

// num_candidates is the ES recall knob (ef_search analog): tune against
// ground truth to the same recall@10 operating point as the SQL backends.
const ES_NUM_CANDIDATES = Number(__ENV.ES_NUM_CANDIDATES || 40);

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
