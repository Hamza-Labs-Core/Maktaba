-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS user_recs (
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_id    TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    score       REAL NOT NULL,
    computed_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (user_id, video_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS user_recs_user_score_idx ON user_recs (user_id, score DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS user_recs_user_score_idx;
DROP TABLE IF EXISTS user_recs;
-- +goose StatementEnd
