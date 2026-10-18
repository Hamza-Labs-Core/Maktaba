-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0103: UUID becomes TEXT, INT INTEGER, DOUBLE PRECISION REAL.
--
CREATE TABLE IF NOT EXISTS books (
    name        TEXT    PRIMARY KEY,
    version     INTEGER NOT NULL,
    title       TEXT    NOT NULL,
    url         TEXT    NOT NULL,
    categories  TEXT    NOT NULL,
    summary     TEXT    NOT NULL,
    checked_at  REAL    NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS series_books (
    season_id  TEXT PRIMARY KEY REFERENCES series_seasons(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    chapter    TEXT NOT NULL,
    fields     TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS series_books;
DROP TABLE IF EXISTS books;
-- +goose StatementEnd
