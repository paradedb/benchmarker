-- ParadeDB setup: table for vector search over SIFT 128d descriptors.
-- vector must exist before pg_search: pg_search registers its bm25 vector
-- opclasses (vector_l2_ops et al) only when pgvector is already present.
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_search;

DROP TABLE IF EXISTS sift CASCADE;

CREATE TABLE sift (
    _id TEXT,
    emb VECTOR(128)
);
