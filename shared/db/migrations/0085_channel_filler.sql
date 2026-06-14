-- +goose Up
-- +goose NO TRANSACTION
--
-- Slot 0085 (Story 27.10 — filler & bumper system) — the short content
-- that pads a channel's slots to the wall-clock boundary so the linear
-- timeline stays contiguous.
--
--   filler_pools  — a named collection of filler items, tied to a channel.
--   filler_items  — a library video designated as filler/bumper/station_id,
--                   with the probed duration the scheduler's fit logic uses.
--
-- `channel_id` references the `channels` table introduced by slot 0081
-- (Epic 27 batch 1). The column carries no hard FK to `channels` so this
-- migration remains runnable even on a branch where 0081 has not landed;
-- the relationship is enforced at the application layer (a FK can be added
-- in a later slot once 0081 is guaranteed present). `video_id` does FK to
-- `videos` (slot 0001) so deleting a video removes it from pools and the
-- next scheduler pass repairs affected schedules (27.10 AC7).
--
-- This migration runs with NO TRANSACTION (the directive above) because
-- Postgres `CREATE INDEX CONCURRENTLY` cannot run inside a transaction
-- block — the project standard for every Postgres-targeted CREATE INDEX
-- (see migrations/README.md §4). Each statement is therefore individually
-- idempotent (IF NOT EXISTS) so a mid-run failure can be re-applied.
--
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS filler_pools (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id  UUID,
    name        TEXT         NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS filler_items (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    pool_id      UUID         NOT NULL REFERENCES filler_pools(id) ON DELETE CASCADE,
    video_id     UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    type         TEXT         NOT NULL DEFAULT 'filler'
        CHECK (type IN ('bumper', 'filler', 'station_id')),
    duration_ms  BIGINT,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (pool_id, video_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS filler_pools_channel_idx ON filler_pools (channel_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS filler_items_pool_idx ON filler_items (pool_id);
-- +goose StatementEnd

-- +goose Down
-- +goose NO TRANSACTION
-- +goose StatementBegin
DROP TABLE IF EXISTS filler_items;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS filler_pools;
-- +goose StatementEnd
