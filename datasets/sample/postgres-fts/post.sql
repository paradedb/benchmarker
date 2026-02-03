-- PostgreSQL FTS post-load: Create GIN index on tsvector
CREATE INDEX documents_content_fts_idx ON documents
USING GIN (to_tsvector('english', content));

CREATE INDEX documents_title_fts_idx ON documents
USING GIN (to_tsvector('english', title));

VACUUM ANALYZE documents;
