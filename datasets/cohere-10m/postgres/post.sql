-- PostgreSQL post-load: build the pgvector HNSW index
SET maintenance_work_mem = '2GB';
SET max_parallel_maintenance_workers = 2;

CREATE INDEX cohere_wiki_emb_idx ON cohere_wiki
USING hnsw (emb vector_cosine_ops)
WITH (m = 16, ef_construction = 64);

-- Recall knob: tune to the recall@10 operating point before publishing numbers.
ALTER DATABASE benchmark SET hnsw.ef_search = 40;

VACUUM ANALYZE cohere_wiki;
