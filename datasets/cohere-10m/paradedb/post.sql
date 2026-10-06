-- ParadeDB post-load: build the pg_search vector index.
DROP INDEX IF EXISTS cohere_wiki_vector_idx;

CREATE INDEX cohere_wiki_vector_idx ON cohere_wiki
USING paradedb (
    _id,
    (text::pdb.unicode_words('stemmer=english', 'stopwords_language=english')),
    emb vector_cosine_ops
) WITH (
    target_segment_count = 10,
    vector_router = 'ivf'
);

-- Recall knob (fraction of the cluster work budget, 1.0 = exhaustive):
-- tune to the recall@10 operating point before publishing numbers.
-- Placeholder until the 10m recall calibration re-runs on this index
-- (0.01 measured 0.950 on the pre-text 10m geometry).
ALTER DATABASE benchmark SET paradedb.vector_cluster_max_probe = 0.01;

VACUUM ANALYZE cohere_wiki;
