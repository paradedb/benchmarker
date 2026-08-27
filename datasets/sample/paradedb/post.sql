-- ParadeDB post-load: Create ParadeDB index
CREATE INDEX documents_search_idx ON documents
USING paradedb (id, title, content)
WITH (key_field='id');

VACUUM ANALYZE documents;
