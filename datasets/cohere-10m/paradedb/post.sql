-- ParadeDB post-load: build the pg_search vector index.
-- `bm25` is the permanent alias for the `paradedb` access method.
CREATE INDEX cohere_wiki_vector_idx ON cohere_wiki
USING bm25 (
    _id,
    (text::pdb.unicode_words('stemmer=english', 'stopwords_language=english')),
    emb vector_cosine_ops
) WITH (
    key_field = '_id',
    centroid_ratio = 0.01,
    target_segment_count = 8,
    cluster_replication = 1
);

-- Recall knob (fraction of the cluster work budget, 1.0 = exhaustive):
-- tune to the recall@10 operating point before publishing numbers.
-- Placeholder until the 10m recall calibration re-runs on this index
-- (0.01 measured 0.950 on the pre-text 10m geometry).
ALTER DATABASE benchmark SET paradedb.vector_cluster_max_probe = 0.01;

VACUUM ANALYZE cohere_wiki;
