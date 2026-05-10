-- +goose Up
-- +goose StatementBegin
-- SQLite FTS5 virtual table mirroring transcript_segments.text. Keeps
-- the same row identity (rowid = transcript_segments.id) so the
-- application-level code can JOIN by rowid with no extra columns.
CREATE VIRTUAL TABLE IF NOT EXISTS transcript_segments_fts
    USING fts5(
        text,
        content='transcript_segments',
        content_rowid='id',
        tokenize='unicode61 remove_diacritics 2'
    );
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS transcript_segments_fts_ai
    AFTER INSERT ON transcript_segments
    BEGIN
        INSERT INTO transcript_segments_fts (rowid, text) VALUES (new.id, new.text);
    END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS transcript_segments_fts_ad
    AFTER DELETE ON transcript_segments
    BEGIN
        INSERT INTO transcript_segments_fts (transcript_segments_fts, rowid, text)
        VALUES ('delete', old.id, old.text);
    END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS transcript_segments_fts_au
    AFTER UPDATE ON transcript_segments
    BEGIN
        INSERT INTO transcript_segments_fts (transcript_segments_fts, rowid, text)
        VALUES ('delete', old.id, old.text);
        INSERT INTO transcript_segments_fts (rowid, text) VALUES (new.id, new.text);
    END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS transcript_segments_fts_au;
DROP TRIGGER IF EXISTS transcript_segments_fts_ad;
DROP TRIGGER IF EXISTS transcript_segments_fts_ai;
DROP TABLE IF EXISTS transcript_segments_fts;
-- +goose StatementEnd
