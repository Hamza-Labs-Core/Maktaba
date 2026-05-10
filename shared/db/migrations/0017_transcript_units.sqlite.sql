-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS transcript_units (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    transcript_id   INTEGER NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    seq             INTEGER NOT NULL CHECK (seq >= 1),
    start_sec       REAL    NOT NULL,
    end_sec         REAL    NOT NULL,
    text            TEXT    NOT NULL,
    language        TEXT    NOT NULL,
    segment_ids     TEXT    NOT NULL,
    indexed_at      TEXT,
    metadata        TEXT    NOT NULL DEFAULT '{}',
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),

    UNIQUE (transcript_id, seq),
    CHECK (start_sec <= end_sec)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcript_units_lang_idx
    ON transcript_units (language);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcript_units_unindexed_idx
    ON transcript_units (transcript_id) WHERE indexed_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcript_units_tid_start_idx
    ON transcript_units (transcript_id, start_sec);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_units_tid_start_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_units_unindexed_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_units_lang_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS transcript_units;
-- +goose StatementEnd
