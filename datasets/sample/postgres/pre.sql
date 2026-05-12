-- PostgreSQL FTS setup: Create table for tsvector search
DROP TABLE IF EXISTS documents CASCADE;

CREATE TABLE documents (
    id UUID PRIMARY KEY,
    title TEXT,
    content TEXT
);
