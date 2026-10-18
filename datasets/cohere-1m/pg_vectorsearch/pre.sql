-- Timescale pg_vectorsearch: pgvector storage with a PRISM index.
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_vectorsearch;

DROP TABLE IF EXISTS cohere_wiki CASCADE;

CREATE TABLE cohere_wiki (
    _id   TEXT,
    url   TEXT,
    title TEXT,
    text  TEXT,
    emb   VECTOR(1024) STORAGE PLAIN
);
