-- ParadeDB setup: table for vector search over Cohere Wikipedia embeddings.
-- Matches paradedb/paradedb benchmarks/datasets/cohere/create_tables.sql.
-- vector must exist before pg_search: pg_search registers its bm25 vector
-- opclasses (vector_cosine_ops et al) only when pgvector is already present.
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_search;

DROP TABLE IF EXISTS cohere_wiki CASCADE;

CREATE TABLE cohere_wiki (
    _id   TEXT,
    url   TEXT,
    title TEXT,
    text  TEXT,
    emb   VECTOR(1024)
);
