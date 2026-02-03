CREATE INDEX documents_content_bm25_idx ON documents
USING bm25 (content) WITH (text_config='english');

VACUUM ANALYZE documents;
