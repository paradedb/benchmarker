import db from "k6/x/database";

// SIFT 10M vector benchmark: unfiltered L2 kNN on the ParadeDB (pg_search)
// vector index, container capped at 8g.

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
  ],
});

const vectors = db.terms(JSON.parse(open("./query_vectors.json")));

const timer = db.timer({ duration: __ENV.DURATION || "30s", gap: "2s" });

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
};

export const collectMetrics = backends.addDockerMetricsCollector(
  scenarios,
  "300s",
);

export const options = { scenarios };

// Keep in sync with the paradedb adapter in measure_recall.py.
const PARADEDB_KNN = `
  SELECT _id
  FROM sift
  WHERE _id @@@ paradedb.all()
  ORDER BY emb <-> $1::vector(128)
  LIMIT 10
`;

export function paradedbKnn() {
  backends.get("paradedb").query(PARADEDB_KNN, vectors.next());
}
