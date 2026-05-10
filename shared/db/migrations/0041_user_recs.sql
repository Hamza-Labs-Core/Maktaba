-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0041 (Epic 7 / Story 7.21) — pre-computed personalised
-- recommendations.
--
-- A nightly Pipeline job populates this table from each user's mean
-- watched-segment embedding. The "For You" rail in
-- `GET /api/recommendations` reads it as the top-K rows by ``score``.
--
CREATE TABLE IF NOT EXISTS user_recs (
    user_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_id    UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    score       REAL         NOT NULL,
    computed_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, video_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS user_recs_user_score_idx
    ON user_recs (user_id, score DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS user_recs_user_score_idx;
DROP TABLE IF EXISTS user_recs;
-- +goose StatementEnd
