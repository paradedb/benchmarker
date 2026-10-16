import db from "k6/x/database";

// Load this dataset with --workload vectorsearch before running.
// Tune recall with VECTORSEARCH_URL's options=-c%20prism.nprobe%3D40.
const backends = db.backends({
  datasetPath: "../",
  backends: [
    {
      type: "postgres",
      workload: "vectorsearch",
      connection: __ENV.VECTORSEARCH_URL,
      container: __ENV.VECTORSEARCH_CONTAINER ?? "vectorsearch",
    },
  ],
});
const vectors = db.terms(JSON.parse(open("./query_vectors.json")));
const duration = __ENV.DURATION || "30s";
const scenarios = {
  vectorsearch_knn: {
    executor: "constant-vus",
    vus: Number(__ENV.VUS || 1),
    duration,
    exec: "vectorsearchKnn",
    tags: { backend: "vectorsearch" },
  },
};
export const collectMetrics = backends.addMetricsCollector(scenarios, duration);
export const options = { scenarios };

export function vectorsearchKnn() {
  backends.get("vectorsearch").query(
    `SELECT _id, title FROM cohere_wiki
     ORDER BY emb <=> $1::vector(1024) LIMIT 10`,
    vectors.next(),
  );
}
