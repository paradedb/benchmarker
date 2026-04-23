import db from "k6/x/database";
import { SharedArray } from "k6/data";
import exec from "k6/execution";

// Configure backends - uses sensible defaults, override as needed
const backends = db.backends({
  backends: ["paradedb"],
});

// Load search terms once, shared across all VUs
const terms = new SharedArray("search_terms", function () {
  return JSON.parse(open("./search_terms.json"));
});

// Get term using scenario-specific iteration number
function getTerm() {
  return terms[exec.vu.iterationInScenario % terms.length];
}

const scenarios = {
  // ParadeDB simple query
  paradedb_simple: {
    executor: "constant-vus",
    vus: 5,
    duration: "500s",
    exec: "paradedbSimpleQuery",
  },
};
export const collectMetrics = backends.addDockerMetricsCollector(scenarios, "500s");

export const options = { scenarios };

// ParadeDB simple query
export function paradedbSimpleQuery() {
  const term = getTerm();
  backends.get("paradedb").search(
    `
    SELECT id, title
    FROM documents
    WHERE content ||| $1
    ORDER BY pdb.score(id) DESC
    LIMIT 10
  `,
    term,
  );
}
