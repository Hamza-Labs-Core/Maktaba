-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0185 (Epic 52): transcripts.revision and
-- transcript_edits (a reviewer's word fixes, with undo and carry-over).
--
ALTER TABLE transcripts ADD COLUMN IF NOT EXISTS revision INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS transcript_edits (
    id            TEXT    PRIMARY KEY,
    transcript_id TEXT    NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    segment_seq   INTEGER NOT NULL,
    char_start    INTEGER NOT NULL,
    char_len      INTEGER NOT NULL,
    before        TEXT    NOT NULL,
    after         TEXT    NOT NULL,
    flag_id       TEXT,
    user_id       TEXT,
    batch_id      TEXT,
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    undone_at     TEXT,
    carried_from  TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcript_edits_segment_idx
    ON transcript_edits (transcript_id, segment_seq);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcript_edits_batch_idx
    ON transcript_edits (batch_id) WHERE batch_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS transcript_edits;
-- +goose StatementEnd
