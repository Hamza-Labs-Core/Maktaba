-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0007 (Story 1.5 / plan-01-05) — soft-delete columns and the
-- partial index that drives the straggler-sweep query.
--
-- `last_seen_at` is the incremental scanner's heartbeat: every visited
-- path advances it. End-of-sweep, anything not advanced is a candidate
-- for `state='missing'`. The partial index on
-- `(library_id, last_seen_at) WHERE state='missing'` keeps that
-- query cheap.
--
-- `deleted_at` is the user-initiated soft-delete column (separate from
-- the scanner's `state='missing'` lifecycle): a video the user
-- explicitly removed via the UI gets `deleted_at = now()` and falls
-- out of all default lists.
--
-- NO TRANSACTION because `CREATE INDEX CONCURRENTLY` cannot run inside
-- a Postgres transaction. The ALTER TABLEs are individually
-- transactional inside Postgres.
--
ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_missing_idx
    ON videos (library_id, last_seen_at)
    WHERE state = 'missing';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_missing_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN IF EXISTS deleted_at;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN IF EXISTS last_seen_at;
-- +goose StatementEnd
