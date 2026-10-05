import db from "k6/x/database";

// SIFT 10M vector benchmark: unfiltered top-10 L2 kNN, ParadeDB (pg_search
// IVF vector index) vs Elasticsearch (dense_vector HNSW). Engines run in
// staggered phases; pick a subset with -e BACKENDS=paradedb.

const BACKENDS = (__ENV.BACKENDS || "paradedb,elasticsearch").split(",");

const backendConfigs = {
  paradedb: {
    type: "paradedb",
    alias: "paradedb",
    connection:
      __ENV.PARADEDB_URL ||
      "postgres://postgres:postgres@localhost:5432/benchmark",
    container: __ENV.PARADEDB_CONTAINER || "paradedb",
    color: "blue",
  },
  elasticsearch: {
    type: "elasticsearch",
    alias: "elasticsearch",
    connection: __ENV.ELASTICSEARCH_URL || "http://localhost:9200",
    container: __ENV.ELASTICSEARCH_CONTAINER || "elasticsearch",
    color: "green",
  },
};

const backends = db.backends({
  datasetPath: "../",
  backends: BACKENDS.map((name) => backendConfigs[name]),
});

const vectors = db.terms(JSON.parse(open("./query_vectors.json")));

const timer = db.timer({ duration: __ENV.DURATION || "300s", gap: "2s" });

// Closed-model VU count; latency claims use 1 (vu1 closed), raise for
// throughput probing (-e VUS=5).
const VUS = Number(__ENV.VUS || 5);

const execs = {
  paradedb: "paradedbKnn",
  elasticsearch: "elasticsearchKnn",
};

const scenarios = {};
BACKENDS.forEach((name, i) => {
  scenarios[`${name}_knn`] = {
    executor: "constant-vus",
    vus: VUS,
    duration: timer.duration(),
    startTime: i === 0 ? timer.get() : timer.advanceAndGet(),
    exec: execs[name],
  };
});

export const collectMetrics = backends.addDockerMetricsCollector(
  scenarios,
  timer,
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

// HNSW recall knob; tune with measure_recall.py --num-candidates.
const ES_NUM_CANDIDATES = Number(__ENV.ES_NUM_CANDIDATES || 100);

// Keep in sync with the elasticsearch adapter in measure_recall.py.
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
    }),
    "sift",
  );
}
