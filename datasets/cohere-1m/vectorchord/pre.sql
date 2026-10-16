-- VectorChord setup for Cohere Wikipedia embeddings.
CREATE EXTENSION IF NOT EXISTS vchord CASCADE;

DROP TABLE IF EXISTS cohere_wiki CASCADE;

CREATE TABLE cohere_wiki (
    _id TEXT,
    url TEXT,
    title TEXT,
    text TEXT,
    emb VECTOR(1024)
);
