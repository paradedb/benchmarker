import { check } from "k6";
import db from "k6/x/database";

// 42 probes measured 95.0% recall@10 on the supplied 100 Cohere queries.
// Remeasure after rebuilding the index; VECTORCHORD_URL can override options.
const backends = db.backends({
  datasetPath: "../",
  backends: [
    {
      type: "vectorchord",
      connection: __ENV.VECTORCHORD_URL,
      container: __ENV.VECTORCHORD_CONTAINER ?? "vectorchord",
    },
  ],
});
const vectors = db.terms(JSON.parse(open("./query_vectors.json")));
const duration = __ENV.DURATION || "30s";
const scenarios = {
  vectorchord_knn: {
    executor: "constant-vus",
    vus: Number(__ENV.VUS || 1),
    duration,
    exec: "vectorchordKnn",
    tags: { backend: "vectorchord" },
  },
};
export const collectMetrics = backends.addMetricsCollector(scenarios, duration);
export const options = {
  scenarios,
  thresholds: { checks: ["rate==1"] },
};

export function vectorchordKnn() {
  const result = backends.get("vectorchord").query(
    `SELECT _id, title FROM cohere_wiki
     ORDER BY emb <=> $1::vector(1024) LIMIT 10`,
    vectors.next(),
  );
  check(result, {
    "query succeeds": (r) => !r.error,
    "returns 10 neighbors": (r) => r.hits === 10,
  });
}
