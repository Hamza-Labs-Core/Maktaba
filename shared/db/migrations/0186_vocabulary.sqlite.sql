-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0186 (Epic 52): vocabulary (what a home admin taught,
-- per library or home, per language) and vocab_pairs (heard X, meant Y). variants is JSON
-- text.
--
CREATE TABLE IF NOT EXISTS vocabulary (
    id         TEXT    PRIMARY KEY,
    scope      TEXT    NOT NULL CHECK (scope IN ('home', 'library')),
    library_id TEXT    REFERENCES libraries(id) ON DELETE CASCADE,
    language   TEXT    NOT NULL,
    term       TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    kind       TEXT    NOT NULL DEFAULT 'word' CHECK (kind IN ('word', 'name', 'term')),
    variants   TEXT    NOT NULL DEFAULT '[]',
    source     TEXT    NOT NULL DEFAULT 'manual' CHECK (source IN ('review', 'manual', 'import')),
    uses       INTEGER NOT NULL DEFAULT 0,
    added_by   TEXT,
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    -- the library, or '' for the home: what the uniqueness reads (NULLs never collide)
    owner      TEXT        GENERATED ALWAYS AS (COALESCE(library_id, '')) STORED,
    CHECK ((scope = 'home') = (library_id IS NULL)),
    UNIQUE (scope, owner, language, key, term)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS vocabulary_key_idx ON vocabulary (key, language);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS vocab_pairs (
    id           TEXT    PRIMARY KEY,
    library_id   TEXT    REFERENCES libraries(id) ON DELETE CASCADE,
    language     TEXT    NOT NULL,
    heard        TEXT    NOT NULL,
    heard_key    TEXT    NOT NULL,
    meant        TEXT    NOT NULL,
    uses         INTEGER NOT NULL DEFAULT 0,
    last_edit_id TEXT,
    owner        TEXT    GENERATED ALWAYS AS (COALESCE(library_id, '')) STORED,
    UNIQUE (owner, language, heard_key, meant)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS vocab_pairs;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS vocabulary;
-- +goose StatementEnd
