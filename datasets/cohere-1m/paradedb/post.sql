DROP INDEX IF EXISTS cohere_wiki_vector_idx;

-- ParadeDB post-load: build the pg_search vector index.
-- `bm25` is the permanent alias for the `paradedb` access method.
CREATE INDEX cohere_wiki_vector_idx ON cohere_wiki
USING bm25 (
    _id,
    (text::pdb.unicode_words('stemmer=english', 'stopwords_language=english')),
    emb vector_cosine_ops
) WITH (
    target_segment_count = 1
);

-- Recall knob (per-segment cluster probe fraction, 1.0 = exhaustive):
-- tune to the recall@10 operating point before publishing numbers.
-- Measured 95.0% recall@10 on the local single-segment pg_search 0.26.0 index with the
-- supplied 100 queries. Remeasure after rebuilding: clustering can vary.
ALTER DATABASE benchmark_1m SET paradedb.vector_cluster_max_probe = 0.0156;

VACUUM ANALYZE cohere_wiki;
