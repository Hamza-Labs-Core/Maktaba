-- +goose Up
-- +goose StatementBegin
--
-- Slot 0104 (parity with mediaintel tag) - an episode of a series tagged by its own title
-- (a Palestine episode of a talk show, a tajweed lesson inside a lecture series) and by its
-- chapter playlist; written by the tag stage, read by the channel planner so themed
-- channels take such episodes one by one.
--
CREATE TABLE IF NOT EXISTS episode_tags (
    video_id  UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    tag       TEXT NOT NULL,
    score     REAL NOT NULL,
    PRIMARY KEY (video_id, tag)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS episode_tags;
-- +goose StatementEnd
