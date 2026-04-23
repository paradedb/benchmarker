import db from "k6/x/database";
import { SharedArray } from "k6/data";
import exec from "k6/execution";

// Configure backends - uses sensible defaults, override as needed
const backends = db.backends({
  backends: [
    "paradedb",
    "elasticsearch",
    "",
    "clickhouse",
    "mongodb",
  ],
});

const loader = db.loader();

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

const timer = db.timer({ duration: "30s", gap: "5s" });

const scenarios = {
  // ParadeDB
  pdb_query: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.advanceAndGet(),
    exec: "pgSimpleQuery",
  },
  pdb_ingest: {
    executor: "constant-vus",
    vus: 2,
    duration: "30s",
    startTime: timer.get(),
    exec: "pgIngest",
  },

  // Elasticsearch
  es_query: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.advanceAndGet(),
    exec: "esSimpleQuery",
  },
  es_ingest: {
    executor: "constant-vus",
    vus: 2,
    duration: "30s",
    startTime: timer.get(),
    exec: "esIngest",
  },

  // 
  _query: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.advanceAndGet(),
    exec: "Simple",
  },
  _ingest: {
    executor: "constant-vus",
    vus: 2,
    duration: "30s",
    startTime: timer.get(),
    exec: "Ingest",
  },

  // ClickHouse
  clickhouse_query: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.advanceAndGet(),
    exec: "clickhouseSimple",
  },
  clickhouse_ingest: {
    executor: "constant-vus",
    vus: 2,
    duration: "30s",
    startTime: timer.get(),
    exec: "clickhouseIngest",
  },

  // MongoDB
  mongodb_query: {
    executor: "constant-vus",
    vus: 5,
    duration: "30s",
    startTime: timer.advanceAndGet(),
    exec: "mongodbSimple",
  },
  mongodb_ingest: {
    executor: "constant-vus",
    vus: 2,
    duration: "30s",
    startTime: timer.get(),
    exec: "mongodbIngest",
  },
};
export const collectMetrics = backends.addDockerMetricsCollector(scenarios, timer);

export const options = { scenarios };

export function setup() {
  console.log(`Loaded ${docs.size()} documents for querying and ingesting`);
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
  const batch = docs.nextBatch(INGEST_BATCH_SIZE);
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
  const batch = docs.nextBatch(INGEST_BATCH_SIZE);
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
  const batch = docs.nextBatch(INGEST_BATCH_SIZE);
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
  const batch = docs.nextBatch(INGEST_BATCH_SIZE);
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
  const batch = docs.nextBatch(INGEST_BATCH_SIZE);
  backends.get("mongodb").insertBatch("documents", batch);
}

export function teardown() {
  // Metrics collection stops when the scenario ends
}
