-- +goose Up
-- +goose StatementBegin
-- Slot 0066 (Epic 14 / Story 14.5) — partial index for Continue
-- Watching. SQLite lacks INCLUDE; the (user_id, updated_at DESC,
-- video_id, position_sec) covering tuple plus the partial WHERE gives
-- the same index-only read for the rail query.
CREATE INDEX IF NOT EXISTS playback_state_continue_partial_idx
    ON playback_state (user_id, updated_at DESC, video_id, position_sec)
    WHERE completed = 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS playback_state_continue_partial_idx;
-- +goose StatementEnd
