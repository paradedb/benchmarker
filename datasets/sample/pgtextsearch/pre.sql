DROP TABLE IF EXISTS documents CASCADE;

CREATE EXTENSION IF NOT EXISTS pg_textsearch;

CREATE TABLE documents (
    id UUID PRIMARY KEY,
    title TEXT,
    content TEXT
);
