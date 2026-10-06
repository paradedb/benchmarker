-- ParadeDB post-load: build the pg_search vector index.
CREATE INDEX cohere_wiki_vector_idx ON cohere_wiki
USING paradedb (
    _id,
    (text::pdb.unicode_words('stemmer=english', 'stopwords_language=english')),
    emb vector_cosine_ops
) WITH (
    target_segment_count = 8,
    vector_router = 'ivf'
);

-- Recall knob (fraction of the cluster work budget, 1.0 = exhaustive):
-- tune to the recall@10 operating point before publishing numbers.
-- Measured 95% recall@10 operating point on the 1m index (see README).
ALTER DATABASE benchmark_1m SET paradedb.vector_cluster_max_probe = 0.035;

VACUUM ANALYZE cohere_wiki;
