-- +goose Up
-- +goose StatementBegin
-- Slot 0235 (Epic 49 story 49.8): keyword search's terms dictionary, as on Postgres (one row
-- per library and term: the key, a spelling as written, the number of documents holding it),
-- and whether a document's words are counted in it. Prefix lookups use the primary key; SQLite
-- has no trigram index, and did-you-mean works out similarity in the API.
CREATE TABLE IF NOT EXISTS search_terms (
    library_id TEXT    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    term       TEXT    NOT NULL,
    shown      TEXT    NOT NULL,
    df         INTEGER NOT NULL,
    PRIMARY KEY (library_id, term)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS search_terms_term ON search_terms (term);
-- +goose StatementEnd

-- +goose StatementBegin
-- the named speakers, per library, by each word start of the name (as on Postgres)
CREATE TABLE IF NOT EXISTS search_speakers (
    library_id TEXT    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    key        TEXT    NOT NULL,
    name       TEXT    NOT NULL,
    df         INTEGER NOT NULL,
    PRIMARY KEY (library_id, key, name)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS search_speakers_key ON search_speakers (key);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS speakers_name_idx ON speakers (name);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE video_search_docs ADD COLUMN IF NOT EXISTS terms_counted INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
-- the keyword_index stage is at version 2 (its documents' words counted: the graph's
-- KEYWORD_INDEX_VERSION), as a freshly migrated database's seed must match the graph
UPDATE job_stages SET version = 2 WHERE stage = 'keyword_index' AND version < 2;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 1 WHERE stage = 'keyword_index' AND version = 2;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE video_search_docs DROP COLUMN terms_counted;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS speakers_name_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS search_speakers;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS search_terms;
-- +goose StatementEnd
