-- +goose Up
-- +goose StatementBegin
--
-- Slot 0107 (parity with mediaintel books) - the book a lesson series explains.
-- books: one row per book name, looked up once in the encyclopedia (an empty title =
-- not found, searched again after books.retry_missing_days). series_books: the book a
-- season was matched to, and the topic fields it gave (comma separated).
--
CREATE TABLE IF NOT EXISTS books (
    name        TEXT             PRIMARY KEY,
    version     INT              NOT NULL,
    title       TEXT             NOT NULL,
    url         TEXT             NOT NULL,
    categories  TEXT             NOT NULL,
    summary     TEXT             NOT NULL,
    checked_at  DOUBLE PRECISION NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS series_books (
    season_id  UUID PRIMARY KEY REFERENCES series_seasons(id) ON DELETE CASCADE,
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
