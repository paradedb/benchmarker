-- Initial partitioning; tune lists and vchordrq.probes against measured recall.
SET maintenance_work_mem = '2GB';
SET max_parallel_maintenance_workers = 2;

CREATE INDEX cohere_wiki_emb_idx ON cohere_wiki
USING vchordrq (emb vector_cosine_ops)
WITH (options = $$
[build.internal]
lists = [2000]
$$);

VACUUM ANALYZE cohere_wiki;

-- Measured recall@10: 0.950 on the supplied 100 Cohere 1M queries.
-- Persist for new connections to whichever database the loader targets.
-- Remeasure after rebuilding: partition training can change recall.
DO $$
BEGIN
    EXECUTE format('ALTER DATABASE %I SET vchordrq.probes = %L', current_database(), '42');
END
$$;
