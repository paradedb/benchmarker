import search from "k6/x/search";
import { SharedArray } from "k6/data";
import exec from "k6/execution";

// Configure backends - uses sensible defaults, override as needed
const backends = search.backends({
  datasetPath: "../",
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

export const options = {
  scenarios: {
    // Metrics collection - covers all phases
    metrics_collector: {
      executor: "constant-vus",
      vus: 1,
      duration: "500s",
      exec: "collectMetrics",
    },

    // ParadeDB simple query
    pg_simple: {
      executor: "constant-vus",
      vus: 5,
      duration: "500s",
      exec: "pgSimpleQuery",
    },
  },
};

export function collectMetrics() {
  backends.collect();
}

// ParadeDB simple query
export function pgSimpleQuery() {
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
