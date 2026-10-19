-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0108: UUID becomes TEXT.
--
CREATE TABLE IF NOT EXISTS episode_tags (
    video_id  TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    tag       TEXT NOT NULL,
    score     REAL NOT NULL,
    PRIMARY KEY (video_id, tag)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS episode_tags;
-- +goose StatementEnd
