-- +goose Up
-- +goose StatementBegin
-- Slot 0232 (Epic 49 story 49.5): one keyword search document per video, as on Postgres, and
-- its FTS5 index: the application writes the normalised title, speakers and body into
-- video_search_docs_fts (api/internal/textnorm's folding; its rowid is the document's), and
-- SQLite ranks with FTS5's bm25(). No word forms on SQLite (§4 of the epic).
CREATE TABLE IF NOT EXISTS video_search_docs (
    video_id      TEXT PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    library_id    TEXT NOT NULL,
    language      TEXT,
    title         TEXT NOT NULL DEFAULT '',
    speakers      TEXT NOT NULL DEFAULT '',
    body          TEXT NOT NULL,
    transcript_id TEXT NOT NULL,
    built_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS video_search_docs_library_idx ON video_search_docs (library_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE VIRTUAL TABLE IF NOT EXISTS video_search_docs_fts
    USING fts5(title, speakers, body, tokenize = 'unicode61 remove_diacritics 2');
-- +goose StatementEnd

-- +goose StatementBegin
-- a document deleted (its video deleted) takes its FTS5 row with it
CREATE TRIGGER IF NOT EXISTS video_search_docs_fts_ad
    AFTER DELETE ON video_search_docs
    BEGIN
        DELETE FROM video_search_docs_fts WHERE rowid = old.rowid;
    END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS video_search_docs_fts_ad;
DROP TABLE IF EXISTS video_search_docs_fts;
DROP TABLE IF EXISTS video_search_docs;
-- +goose StatementEnd
