-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0081 (Epic 27 / Story 27.1).
--
-- Substitutions (migrations/README.md §3):
--   UUID         → TEXT (app generates the id)
--   JSONB        → TEXT (JSON-encoded)
--   TIMESTAMPTZ  → TEXT (ISO-8601)
--   BOOLEAN      → INTEGER (0/1)
--
-- Column-level CHECKs port verbatim. The scope-uniqueness uses the same
-- COALESCE-with-sentinel expression index as the Postgres slot; the
-- empty-string sentinel stands in for the multi-library (null) bucket
-- (the recommendation_dismissals slot-0067 pattern).
--
CREATE TABLE IF NOT EXISTS channels (
    id            TEXT    PRIMARY KEY,
    library_id    TEXT    REFERENCES libraries(id) ON DELETE CASCADE,
    number        INTEGER NOT NULL,
    name          TEXT    NOT NULL,
    slug          TEXT    NOT NULL,
    logo_path     TEXT,
    category      TEXT    NOT NULL DEFAULT 'general',
    mode          TEXT    NOT NULL
                          CHECK (mode IN ('shuffle','marathon','schedule','smart_mix')),
    mode_config   TEXT    NOT NULL DEFAULT '{}',
    source_filter TEXT,
    transition    TEXT    NOT NULL DEFAULT 'cut'
                          CHECK (transition IN ('cut','crossfade')),
    enabled       INTEGER NOT NULL DEFAULT 1,
    sort_order    INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS channels_scope_number_uniq
    ON channels (COALESCE(library_id, ''), number);
CREATE UNIQUE INDEX IF NOT EXISTS channels_scope_slug_uniq
    ON channels (COALESCE(library_id, ''), slug);
CREATE INDEX IF NOT EXISTS channels_enabled_idx ON channels (enabled, sort_order, number);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS channels_enabled_idx;
DROP INDEX IF EXISTS channels_scope_slug_uniq;
DROP INDEX IF EXISTS channels_scope_number_uniq;
DROP TABLE IF EXISTS channels;
-- +goose StatementEnd
