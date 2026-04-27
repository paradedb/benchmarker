import db from "k6/x/database";

const backends = db.backends({
  datasetPath: "../",
  backends: ["paradedb"],
});

const terms = db.terms(JSON.parse(open("./search_terms.json")));

const scenarios = {
  paradedb_simple: {
    executor: "constant-vus",
    vus: 5,
    duration: "500s",
    exec: "paradedbSimpleQuery",
  },
};
export const collectMetrics = backends.addDockerMetricsCollector(scenarios, "500s");

export const options = { scenarios };

export function paradedbSimpleQuery() {
  backends.get("paradedb").query(
    `
    SELECT id, title
    FROM documents
    WHERE content ||| $1
    ORDER BY pdb.score(id) DESC
    LIMIT 10
  `,
    terms.next(),
  );
}
