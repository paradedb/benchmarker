import db from "k6/x/database";

const backends = db.backends({
  datasetPath: "../",
  backends: ["paradedb", "postgres"],
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
};

export const collectMetrics = backends.addDockerMetricsCollector(
  scenarios,
  "140s",
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
