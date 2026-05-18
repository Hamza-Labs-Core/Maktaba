-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0064 (Epic 14 / Story 14.7) — persisted "Not interested"
-- dismissals.
--
-- Two dismissal scopes:
--   * a whole reason_kind row (e.g. the user never wants the
--     "because-you-watched" rail again), recorded with video_id NULL;
--   * a single video within recommendations, recorded with a
--     non-NULL video_id (reason_kind carries the rail it was hidden
--     from, or '' for an item-level global hide).
--
-- Both DELETE endpoints upsert here; the recommendations composer
-- filters its output against this table so a hide persists across
-- devices and sessions. CASCADE on user/video keeps it tidy.
--
CREATE TABLE IF NOT EXISTS recommendation_dismissals (
    user_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason_kind TEXT         NOT NULL DEFAULT '',
    video_id    UUID         REFERENCES videos(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- One row dismissal per (user, reason_kind) and one item dismissal per
-- (user, video). COALESCE keeps the two scopes from colliding on the
-- unique index when video_id is NULL.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS recommendation_dismissals_uniq_idx
    ON recommendation_dismissals
       (user_id, reason_kind, COALESCE(video_id, '00000000-0000-0000-0000-000000000000'::uuid));
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS recommendation_dismissals_user_idx
    ON recommendation_dismissals (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS recommendation_dismissals_user_idx;
DROP INDEX IF EXISTS recommendation_dismissals_uniq_idx;
DROP TABLE IF EXISTS recommendation_dismissals;
-- +goose StatementEnd
