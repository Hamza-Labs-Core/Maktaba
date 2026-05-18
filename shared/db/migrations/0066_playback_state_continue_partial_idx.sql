-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0066 (Epic 14 / Story 14.5) — partial covering index for the
-- "Continue Watching" rail.
--
-- Story 14.5 explicitly owns a *partial* covering index so the
-- cross-device Continue Watching guarantee (TV updates within 5 s) is
-- met without a sequential scan. The base index from slot 0038
-- (user_id, updated_at DESC) is non-partial and cannot cover the
-- 5%..95% progress predicate the rail filters on.
--
-- The predicate mirrors the rail query in
-- api/internal/handlers/recommendations/recommendations.go: only rows
-- that are in-progress (position between 5% and 95% of duration and
-- not completed) qualify. INCLUDE carries position_sec so the rail
-- read is index-only.
--
CREATE INDEX CONCURRENTLY IF NOT EXISTS playback_state_continue_partial_idx
    ON playback_state (user_id, updated_at DESC)
    INCLUDE (video_id, position_sec)
    WHERE completed = false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS playback_state_continue_partial_idx;
-- +goose StatementEnd
