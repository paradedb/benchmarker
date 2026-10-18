-- Build after loading so PRISM sizes its partitions from the row count.
SET maintenance_work_mem = '2GB';
SET max_parallel_maintenance_workers = 2;

CREATE INDEX cohere_wiki_emb_idx ON cohere_wiki
USING prism (emb vec32_cosine_ops);

VACUUM ANALYZE cohere_wiki;
