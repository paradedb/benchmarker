-- PostgreSQL + pgvector setup: table for vector search over Cohere Wikipedia embeddings.
-- Matches paradedb/paradedb benchmarks/datasets/cohere/create_tables.sql.
CREATE EXTENSION IF NOT EXISTS vector;

DROP TABLE IF EXISTS cohere_wiki CASCADE;

CREATE TABLE cohere_wiki (
    _id   TEXT,
    url   TEXT,
    title TEXT,
    text  TEXT,
    emb   VECTOR(1024)
);
