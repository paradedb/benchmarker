-- ParadeDB post-load: Create BM25 index
CREATE INDEX documents_search_idx ON documents
USING bm25 (id, title, content)
WITH (key_field='id');

VACUUM ANALYZE documents;
