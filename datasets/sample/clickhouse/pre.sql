DROP TABLE IF EXISTS documents;
SET enable_full_text_index = true;

CREATE TABLE documents (
    id String,
    title String,
    content String,
    INDEX content_idx(content) TYPE text(tokenizer = splitByNonAlpha)
) ENGINE = MergeTree()
ORDER BY id
