import { check } from "k6";
import exec from "k6/execution";
import db from "k6/x/database";

// Load each target with --backend <name> before running this comparison.
// Connection defaults and extension capture come from the profile manifests.
const names = ["paradedb", "vectorsearch", "vectorchord"];
const backends = db.backends({
  datasetPath: "../",
  backends: names.map((profile) => ({
    type: profile,
    connection: __ENV[`${profile.toUpperCase()}_URL`],
    container: __ENV[`${profile.toUpperCase()}_CONTAINER`] ?? profile,
  })),
});
const vectors = db.terms(JSON.parse(open("./query_vectors.json")));
const timer = db.timer({ duration: __ENV.DURATION || "30s", gap: "2s" });
const scenarios = {};
for (const [i, profile] of names.entries()) {
  scenarios[profile] = {
    executor: "constant-vus",
    vus: Number(__ENV.VUS || 1),
    duration: timer.duration(),
    startTime: i === 0 ? timer.get() : timer.advanceAndGet(),
    exec: "knn",
    tags: { backend: profile },
  };
}
export const collectMetrics = backends.addMetricsCollector(scenarios, timer);
export const options = {
  scenarios,
  thresholds: { checks: ["rate==1"] },
};

export function knn() {
  const profile = exec.scenario.name;
  const filter = profile === "paradedb" ? "WHERE _id @@@ paradedb.all()" : "";
  const result = backends.get(profile).query(
    `SELECT _id, title FROM cohere_wiki
     ${filter}
     ORDER BY emb <=> $1::vector(1024) LIMIT 10`,
    vectors.next(),
  );
  check(result, {
    "query succeeds": (r) => !r.error,
    "returns 10 neighbors": (r) => r.hits === 10,
  });
}
