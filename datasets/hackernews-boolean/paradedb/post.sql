\set ON_ERROR_STOP on
CREATE INDEX hn_boolean_idx ON hn_items
USING bm25 (
    id, (title::pdb.unicode_words), (text::pdb.unicode_words),
    time, score, (type::pdb.literal), (by::pdb.literal),
    parent, descendants, dead, deleted
)
WITH (target_segment_count = 8, partition_by = 'ctid');
ANALYZE hn_items;
