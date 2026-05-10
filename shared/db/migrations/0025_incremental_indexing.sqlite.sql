-- +goose Up
-- +goose StatementBegin
ALTER TABLE transcript_units ADD COLUMN indexed_at_in_chroma TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcript_units_unindexed_chroma_idx
    ON transcript_units (transcript_id) WHERE indexed_at_in_chroma IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS vector_index_dead_letter (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    unit_id            INTEGER NOT NULL REFERENCES transcript_units(id) ON DELETE CASCADE,
    library_id         TEXT    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    transcript_id      INTEGER NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    attempts           INTEGER NOT NULL DEFAULT 0,
    last_error         TEXT,
    last_attempted_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    created_at         TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (unit_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS vector_dlq_library_idx
    ON vector_index_dead_letter (library_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS vector_dlq_library_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS vector_index_dead_letter;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_units_unindexed_chroma_idx;
-- +goose StatementEnd

-- +goose StatementBegin
-- SQLite cannot DROP COLUMN cleanly until 3.35; keep the column on
-- rollback rather than rebuild the whole table.
SELECT 1;
-- +goose StatementEnd
