import db from "k6/x/database";
import { check } from "k6";
import exec from "k6/execution";

// Prerequisite: load datasets/sample into ParadeDB (including post.sql).
// Fresh IDs allow inserts into the loaded table and on subsequent runs.
const table = __ENV.TABLE || "documents";
if (!/^[a-z_][a-z0-9_]*$/.test(table)) {
  throw new Error("TABLE must be a simple lowercase SQL table name");
}
const duration = __ENV.DURATION || "30s";
const backends = db.backends({
  backends: [
    {
      type: "paradedb",
      connection:
        __ENV.PARADEDB_URL ||
        "postgres://postgres:postgres@localhost:5432/benchmark",
    },
  ],
});
const terms = db.terms(JSON.parse(open("./search_terms.json")));
const stream = db.loader().openDocumentStream(__ENV.DATA_FILE || "../data.csv", {
  schema: __ENV.SCHEMA_FILE || "../schema.yaml",
});

export const options = {
  thresholds: { checks: ["rate==1"] },
  scenarios: {
    searches: {
      executor: "constant-vus",
      vus: 5,
      duration,
      exec: "search",
    },
    inserts: {
      executor: "per-vu-iterations",
      vus: 1,
      iterations: 1,
      maxDuration: duration,
      gracefulStop: "0s",
      exec: "insert",
    },
  },
};

export function search() {
  const result = backends.get("paradedb").query(
    `SELECT id, title FROM ${table}
     WHERE content ||| $1
     ORDER BY pdb.score(id) DESC LIMIT 10`,
    terms.next(),
  );
  check(result, { "search succeeded": (r) => !r.error });
}

export function insert() {
  const backend = backends.get("paradedb");
  try {
    while (true) {
      const batch = stream.nextBatch(100);
      if (batch.length === 0) return;
      for (const document of batch) {
        document.id = crypto.randomUUID();
      }
      const result = backend.insertBatch(table, batch);
      if (
        !check(result, {
          "insert batch succeeded": (r) => !r.error && r.rows === batch.length,
        })
      ) {
        throw new Error(result.error || "unexpected row count");
      }
    }
  } catch (error) {
    exec.test.abort(`Insert workload failed: ${error}`);
  } finally {
    stream.close();
  }
}
