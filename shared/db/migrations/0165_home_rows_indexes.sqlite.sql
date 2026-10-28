-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0165: the indexes the home's rows read (Recently
-- Added's whole order on the visible videos, across libraries and in one; the series
-- newest first).
--
CREATE INDEX IF NOT EXISTS videos_recent_idx
    ON videos (created_at DESC, mtime DESC, id DESC)
    WHERE deleted_at IS NULL AND state NOT IN ('missing', 'superseded');
CREATE INDEX IF NOT EXISTS videos_library_recent_idx
    ON videos (library_id, created_at DESC, mtime DESC, id DESC)
    WHERE deleted_at IS NULL AND state NOT IN ('missing', 'superseded');
CREATE INDEX IF NOT EXISTS series_created_idx
    ON series (created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS series_created_idx;
DROP INDEX IF EXISTS videos_library_recent_idx;
DROP INDEX IF EXISTS videos_recent_idx;
-- +goose StatementEnd
