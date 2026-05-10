-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0013.
-- SQLite has no PL/pgSQL — `commit_segment(...)` is implemented as
-- application-side INSERT in `pipeline/src/maktaba_pipeline/db/segments.py`.
-- The NOTIFY equivalent is published via the in-process pubsub bus by
-- the same helper (mirrors the Postgres trigger semantics).
--
CREATE TABLE IF NOT EXISTS transcript_segments (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    transcript_id   INTEGER NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    seq             INTEGER NOT NULL CHECK (seq >= 1),
    start_sec       REAL    NOT NULL CHECK (start_sec >= 0),
    end_sec         REAL    NOT NULL,
    text            TEXT    NOT NULL,
    speaker         TEXT,
    confidence      REAL,
    committed_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (transcript_id, seq),
    CHECK (start_sec <= end_sec)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcript_segments_tid_seq_idx
    ON transcript_segments (transcript_id, seq);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcript_segments_tid_start_idx
    ON transcript_segments (transcript_id, start_sec);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_segments_tid_start_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_segments_tid_seq_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS transcript_segments;
-- +goose StatementEnd
