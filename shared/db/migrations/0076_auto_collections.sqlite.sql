-- +goose Up
-- +goose StatementBegin
-- SQLite parity sibling for slot 0076 (Story 26.4). SQLite ADD COLUMN
-- has no IF NOT EXISTS; the migration runs once so a plain ADD is safe.
ALTER TABLE collections ADD COLUMN IF NOT EXISTS origin TEXT NOT NULL DEFAULT 'user';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections ADD COLUMN IF NOT EXISTS auto_rule TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections ADD COLUMN IF NOT EXISTS dismissed_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS collection_suggestions (
    id          TEXT    PRIMARY KEY,
    library_id  TEXT    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name        TEXT    NOT NULL,
    kind        TEXT    NOT NULL,
    smart_query TEXT    NOT NULL DEFAULT '{}',
    status      TEXT    NOT NULL DEFAULT 'suggested'
                        CHECK (status IN ('suggested','accepted','dismissed')),
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    decided_at  TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS collection_suggestions_library_idx
    ON collection_suggestions (library_id, status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS collection_suggestions_library_idx;
DROP TABLE IF EXISTS collection_suggestions;
ALTER TABLE collections DROP COLUMN dismissed_at;
ALTER TABLE collections DROP COLUMN auto_rule;
ALTER TABLE collections DROP COLUMN origin;
-- +goose StatementEnd
