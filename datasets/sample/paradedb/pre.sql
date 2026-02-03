-- ParadeDB setup: Create table for BM25 search
CREATE EXTENSION IF NOT EXISTS pg_search;

DROP TABLE IF EXISTS documents CASCADE;

CREATE TABLE documents (
    id UUID PRIMARY KEY,
    title TEXT,
    content TEXT
);
