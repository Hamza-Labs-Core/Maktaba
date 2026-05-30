-- +goose Up
-- +goose StatementBegin
-- Slot 0067 (Epic 14 / Story 14.7) — persisted "Not interested"
-- dismissals. SQLite mirror of the Postgres slot; empty-string sentinel
-- replaces the all-zero UUID for the NULL video scope.
CREATE TABLE IF NOT EXISTS recommendation_dismissals (
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason_kind TEXT NOT NULL DEFAULT '',
    video_id    TEXT REFERENCES videos(id) ON DELETE CASCADE,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS recommendation_dismissals_uniq_idx
    ON recommendation_dismissals
       (user_id, reason_kind, COALESCE(video_id, ''));
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS recommendation_dismissals_user_idx
    ON recommendation_dismissals (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS recommendation_dismissals_user_idx;
DROP INDEX IF EXISTS recommendation_dismissals_uniq_idx;
DROP TABLE IF EXISTS recommendation_dismissals;
-- +goose StatementEnd
