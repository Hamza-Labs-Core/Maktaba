-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS search_suggestion_terms (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    library_id      TEXT    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    term            TEXT    NOT NULL,
    term_normalized TEXT    NOT NULL,
    ngram           INTEGER NOT NULL CHECK (ngram BETWEEN 2 AND 4),
    frequency       INTEGER NOT NULL CHECK (frequency >= 1),
    doc_frequency   INTEGER NOT NULL CHECK (doc_frequency >= 1),
    last_seen_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (library_id, term_normalized)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- SQLite does not have text_pattern_ops; the default collation handles
-- LIKE 'prefix%' against a btree index well enough for typeahead.
CREATE INDEX IF NOT EXISTS search_suggestion_terms_prefix_idx
    ON search_suggestion_terms (library_id, term_normalized);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS search_suggestion_terms_prefix_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS search_suggestion_terms;
-- +goose StatementEnd
