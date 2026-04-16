import search from "k6/x/search";
import { SharedArray } from "k6/data";
import exec from "k6/execution";

// Configure backends - uses sensible defaults, override as needed
const backends = search.backends({
  datasetPath: "../",
  backends: [
    "paradedb",
    "elasticsearch",
    "",
    "clickhouse",
    "mongodb",
  ],
});

const loader = search.loader();

// Load search terms once, shared across all VUs
const terms = new SharedArray("search_terms", function () {
  return JSON.parse(open("./search_terms.json"));
});

// Load documents once on Go side - shared across all VUs
const DATA_FILE = __ENV.DATA_FILE || "../data.csv";
const docs = loader.openDocuments(DATA_FILE);

// Ingest batch size
const INGEST_BATCH_SIZE = 1000;

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
      duration: "3m",
      exec: "collectMetrics",
    },

    // ==================== ParadeDB ====================
    // ParadeDB query: 0s - 30s
    pdb_query: {
      executor: "constant-vus",
      vus: 5,
      duration: "30s",
      exec: "pgSimpleQuery",
    },
    // ParadeDB ingest: 0s - 30s (parallel with query)
    pdb_ingest: {
      executor: "constant-vus",
      vus: 2,
      duration: "30s",
      exec: "pgIngest",
    },

    // ==================== Elasticsearch ====================
    // Elasticsearch query: 35s - 65s
    es_query: {
      executor: "constant-vus",
      vus: 5,
      duration: "30s",
      startTime: "35s",
      exec: "esSimpleQuery",
    },
    // Elasticsearch ingest: 35s - 65s (parallel with query)
    es_ingest: {
      executor: "constant-vus",
      vus: 2,
      duration: "30s",
      startTime: "35s",
      exec: "esIngest",
    },

    // ====================  ====================
    //  query: 70s - 100s
    _query: {
      executor: "constant-vus",
      vus: 5,
      duration: "30s",
      startTime: "70s",
      exec: "Simple",
    },
    //  ingest: 70s - 100s (parallel with query)
    _ingest: {
      executor: "constant-vus",
      vus: 2,
      duration: "30s",
      startTime: "70s",
      exec: "Ingest",
    },

    // ==================== ClickHouse ====================
    // ClickHouse query: 105s - 135s
    clickhouse_query: {
      executor: "constant-vus",
      vus: 5,
      duration: "30s",
      startTime: "105s",
      exec: "clickhouseSimple",
    },
    // ClickHouse ingest: 105s - 135s (parallel with query)
    clickhouse_ingest: {
      executor: "constant-vus",
      vus: 2,
      duration: "30s",
      startTime: "105s",
      exec: "clickhouseIngest",
    },

    // ==================== MongoDB ====================
    // MongoDB query: 140s - 170s
    mongodb_query: {
      executor: "constant-vus",
      vus: 5,
      duration: "30s",
      startTime: "140s",
      exec: "mongodbSimple",
    },
    // MongoDB ingest: 140s - 170s (parallel with query)
    mongodb_ingest: {
      executor: "constant-vus",
      vus: 2,
      duration: "30s",
      startTime: "140s",
      exec: "mongodbIngest",
    },
  },
};

export function setup() {
  console.log(`Loaded ${docs.size()} documents for querying and ingesting`);
}

export function collectMetrics() {
  backends.collect();
}

// ==================== ParadeDB ====================
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

export function pgIngest() {
  const batch = docs.nextBatchNewIds(INGEST_BATCH_SIZE);
  backends.get("paradedb").insertBatch("documents", batch);
}

// ==================== Elasticsearch ====================
export function esSimpleQuery() {
  const term = getTerm();
  backends.get("elasticsearch").search("documents", {
    query: {
      match: { content: term },
    },
    _source: ["id", "title"],
    size: 10,
  });
}

export function esIngest() {
  const batch = docs.nextBatchNewIds(INGEST_BATCH_SIZE);
  backends.get("elasticsearch").insertBatch("documents", batch);
}

// ====================  ====================
export function Simple() {
  const term = getTerm();
  backends.get("").search(
    `
    SELECT id, title
    FROM documents
    ORDER BY content <@> $1
    LIMIT 10
  `,
    term,
  );
}

export function Ingest() {
  const batch = docs.nextBatchNewIds(INGEST_BATCH_SIZE);
  backends.get("").insertBatch("documents", batch);
}

// ==================== ClickHouse ====================
export function clickhouseSimple() {
  const term = getTerm();
  backends.get("clickhouse").search(
    `
    SELECT id, title
    FROM documents
    WHERE hasToken(content, ?)
    LIMIT 10
  `,
    term,
  );
}

export function clickhouseIngest() {
  const batch = docs.nextBatchNewIds(INGEST_BATCH_SIZE);
  backends.get("clickhouse").insertBatch("documents", batch);
}

// ==================== MongoDB ====================
export function mongodbSimple() {
  const term = getTerm();
  backends.get("mongodb").search(
    JSON.stringify({
      text: { query: term, path: ["title", "content"] },
    }),
    "documents",
  );
}

export function mongodbIngest() {
  const batch = docs.nextBatchNewIds(INGEST_BATCH_SIZE);
  backends.get("mongodb").insertBatch("documents", batch);
}

export function teardown() {
  // Metrics collection stops when the scenario ends
}
