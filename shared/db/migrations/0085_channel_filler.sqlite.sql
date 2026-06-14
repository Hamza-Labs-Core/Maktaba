-- +goose Up
-- +goose StatementBegin
-- Slot 0085 (Story 27.10) — SQLite mirror of the filler/bumper tables.
-- See the Postgres pair for the rationale; `channel_id` carries no FK to
-- `channels` (slot 0081, Epic 27 batch 1) so this runs standalone.
CREATE TABLE IF NOT EXISTS filler_pools (
    id          TEXT PRIMARY KEY,
    channel_id  TEXT,
    name        TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS filler_pools_channel_idx ON filler_pools (channel_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS filler_items (
    id           TEXT PRIMARY KEY,
    pool_id      TEXT NOT NULL REFERENCES filler_pools(id) ON DELETE CASCADE,
    video_id     TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    type         TEXT NOT NULL DEFAULT 'filler'
        CHECK (type IN ('bumper', 'filler', 'station_id')),
    duration_ms  INTEGER,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (pool_id, video_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS filler_items_pool_idx ON filler_items (pool_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS filler_items;
DROP TABLE IF EXISTS filler_pools;
-- +goose StatementEnd
