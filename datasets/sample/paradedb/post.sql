-- ParadeDB post-load: Create ParadeDB index
DROP INDEX IF EXISTS documents_search_idx;

CREATE INDEX documents_search_idx ON documents
USING paradedb (id, title, content);

VACUUM ANALYZE documents;
