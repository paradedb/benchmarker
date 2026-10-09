import exec from "k6/execution";
import { Counter } from "k6/metrics";
import db from "k6/x/database";
import { request, selectCases, shuffled } from "./queries.js";

if (!__ENV.PARADEDB_URL) throw new Error("PARADEDB_URL is required");
const mode = __ENV.MODE || "count";
const vus = Number(__ENV.VUS || 1);
const duration = Number(__ENV.DURATION_SECONDS || 300);
const warmup = Number(__ENV.WARMUP_SECONDS || 30);
const topK = Number(__ENV.TOP_K || 10);
const seed = Number(__ENV.SEED || 20261008);
const passes = Number(__ENV.PASSES || 0);
if (
  ![vus, duration, topK].every(
    (value) => Number.isSafeInteger(value) && value > 0,
  ) ||
  ![seed, warmup, passes].every(
    (value) => Number.isSafeInteger(value) && value >= 0,
  )
)
  throw new Error("Invalid numeric workload option");
if (passes && warmup)
  throw new Error(
    "PASSES requires WARMUP_SECONDS=0; run a separate warmup pass first",
  );
const queries = shuffled(
  selectCases(
    JSON.parse(open(__ENV.QUERIES || "../artifacts/validated-queries.json")),
    mode,
    {
      shape: __ENV.SHAPE,
      mix: __ENV.MIX,
      profile: __ENV.PROFILE,
    },
  ),
  seed,
);
const requests = queries.map((query) => request(query, mode, topK));
const backends = db.backends({
  datasetPath: "..",
  backends: [
    {
      type: "paradedb",
      connection: __ENV.PARADEDB_URL,
      container: __ENV.PARADEDB_CONTAINER || "",
    },
  ],
});
backends.setTimeout(Number(__ENV.QUERY_TIMEOUT_SECONDS || 30));
const tags = {
  backend: "paradedb",
  chart: `hn_boolean_${mode}`,
  ...(warmup ? { warmup: "true" } : {}),
};
const scenarios = {
  search: passes
    ? {
        executor: "shared-iterations",
        vus,
        iterations: queries.length * passes,
        maxDuration: `${duration}s`,
        exec: "search",
        tags,
      }
    : {
        executor: "ramping-vus",
        startVUs: vus,
        stages: [
          ...(warmup ? [{ duration: `${warmup}s`, target: vus }] : []),
          { duration: `${duration}s`, target: vus },
        ],
        gracefulRampDown: "0s",
        exec: "search",
        tags,
      },
};
export const collectMetrics =
  __ENV.TELEMETRY === "1"
    ? backends.addMetricsCollector(scenarios, `${warmup + duration}s`)
    : () => {};
const errors = new Counter("benchmark_query_errors");
export const options = {
  scenarios,
  thresholds: { benchmark_query_errors: ["count==0"] },
};

export function search() {
  const index = Number(exec.scenario.iterationInTest) % queries.length;
  const query = queries[index];
  Object.assign(exec.vu.metrics.tags, {
    query_id: `${query.id}:${mode}${mode === "count" ? "" : `:k${topK}`}`,
    shape: query.shape,
    mix: query.mix,
    profile: query.profile,
  });
  const result = backends.get("paradedb").query(...requests[index]);
  errors.add(result?.error && !result?.deadlineReached ? 1 : 0);
}
