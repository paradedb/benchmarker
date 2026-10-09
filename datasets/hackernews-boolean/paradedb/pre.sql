\set ON_ERROR_STOP on
CREATE EXTENSION IF NOT EXISTS pg_search;
CREATE TABLE IF NOT EXISTS hn_items (
    id bigint PRIMARY KEY,
    deleted boolean,
    type text,
    "by" text,
    time timestamptz,
    text text,
    dead boolean,
    parent bigint,
    poll bigint,
    kids bigint[],
    url text,
    score integer,
    title text,
    parts bigint[],
    descendants integer
);
