import db from "k6/x/database";

const backends = db.backends({
  datasetPath: "../",
  backends: [
    "paradedb",
    "elasticsearch",
    "clickhouse",
    "mongodb",
  ],
});

const terms = db.terms(JSON.parse(open("./search_terms.json")));
const loader = db.loader();
const docs = loader.openDocuments(__ENV.DATA_FILE || "../data.csv");
const INGEST_BATCH_SIZE = 1000;

const timer = db.timer({ duration: "30s", gap: "5s" });

const scenarios = {
  // ParadeDB
  pdb_query: {
    executor: "constant-vus",
    vus: 5,
    duration: timer.duration(),
    startTime: timer.get(),
    exec: "pgSimpleQuery",
  },
  pdb_ingest: {
    executor: "constant-vus",
    vus: 2,
    duration: timer.duration(),
    startTime: timer.get(),
    exec: "pgIngest",
  },

  // Elasticsearch
  es_query: {
    executor: "constant-vus",
    vus: 5,
    duration: timer.duration(),
    startTime: timer.advanceAndGet(),
    exec: "esSimpleQuery",
  },
  es_ingest: {
    executor: "constant-vus",
    vus: 2,
    duration: timer.duration(),
    startTime: timer.get(),
    exec: "esIngest",
  },

  // ClickHouse
  clickhouse_query: {
    executor: "constant-vus",
    vus: 5,
    duration: timer.duration(),
    startTime: timer.advanceAndGet(),
    exec: "clickhouseSimple",
  },
  clickhouse_ingest: {
    executor: "constant-vus",
    vus: 2,
    duration: timer.duration(),
    startTime: timer.get(),
    exec: "clickhouseIngest",
  },

  // MongoDB
  mongodb_query: {
    executor: "constant-vus",
    vus: 5,
    duration: timer.duration(),
    startTime: timer.advanceAndGet(),
    exec: "mongodbSimple",
  },
  mongodb_ingest: {
    executor: "constant-vus",
    vus: 2,
    duration: timer.duration(),
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

export function pgIngest() {
  const batch = docs.nextBatch(INGEST_BATCH_SIZE, "paradedb");
  backends.get("paradedb").insertBatch("documents", batch);
}

// ==================== Elasticsearch ====================
export function esSimpleQuery() {
  backends.get("elasticsearch").query("documents", {
    query: {
      match: { content: terms.next() },
    },
    _source: ["id", "title"],
    size: 10,
  });
}

export function esIngest() {
  const batch = docs.nextBatch(INGEST_BATCH_SIZE, "elasticsearch");
  backends.get("elasticsearch").insertBatch("documents", batch);
}

// ==================== ClickHouse ====================
export function clickhouseSimple() {
  backends.get("clickhouse").query(
    `
    SELECT id, title
    FROM documents
    WHERE hasToken(content, ?)
    LIMIT 10
  `,
    terms.next(),
  );
}

export function clickhouseIngest() {
  const batch = docs.nextBatch(INGEST_BATCH_SIZE, "clickhouse");
  backends.get("clickhouse").insertBatch("documents", batch);
}

// ==================== MongoDB ====================
export function mongodbSimple() {
  backends.get("mongodb").query(
    JSON.stringify({
      text: { query: terms.next(), path: ["title", "content"] },
    }),
    "documents",
  );
}

export function mongodbIngest() {
  const batch = docs.nextBatch(INGEST_BATCH_SIZE, "mongodb");
  backends.get("mongodb").insertBatch("documents", batch);
}
