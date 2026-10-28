-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0165: the home's rows read the first rows of an index, not the whole table.
--
-- Recently Added orders the visible videos by (created_at DESC, mtime DESC, id DESC).
-- Slot 0159's (created_at DESC, id DESC) and (library_id, created_at DESC) stop at
-- created_at, so a home whose videos were all added at once (the largest: 150k videos,
-- one created_at) sorted the whole table for every such row: 110 to 190 ms each for
-- the home's, the hub's and each pinned library's. These two carry the whole order,
-- on the visible videos only (the rows' predicate):
--
--   videos_recent_idx          the home's row (every library, or a few)
--   videos_library_recent_idx  a library's row: the hub, a pinned library on the home
--
-- The home's featured series is the newest series with a visible lesson:
-- series_created_idx walks the series newest first and stops at the first one.
--
-- NO TRANSACTION for the indexes (CONCURRENTLY: the table is large), as slot 0159.
-- Additive.
--
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_recent_idx
    ON videos (created_at DESC, mtime DESC, id DESC)
    WHERE deleted_at IS NULL AND state NOT IN ('missing', 'superseded');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_library_recent_idx
    ON videos (library_id, created_at DESC, mtime DESC, id DESC)
    WHERE deleted_at IS NULL AND state NOT IN ('missing', 'superseded');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS series_created_idx
    ON series (created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS series_created_idx;
DROP INDEX IF EXISTS videos_library_recent_idx;
DROP INDEX IF EXISTS videos_recent_idx;
-- +goose StatementEnd
