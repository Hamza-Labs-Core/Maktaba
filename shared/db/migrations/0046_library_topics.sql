-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0046 (Epic 9 / Story 9.9) — per-library topic clusters.
--
-- `centroid_vec` is a packed float32 array (numpy's tobytes()).
--
CREATE TABLE IF NOT EXISTS library_topics (
    library_id     UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    topic_id       INTEGER      NOT NULL,
    label          TEXT,
    centroid_vec   BYTEA        NOT NULL,
    video_count    INTEGER      NOT NULL DEFAULT 0,
    computed_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (library_id, topic_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS video_topics (
    video_id    UUID    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    library_id  UUID    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    topic_id    INTEGER NOT NULL,
    score       REAL    NOT NULL,
    PRIMARY KEY (video_id, topic_id),
    FOREIGN KEY (library_id, topic_id) REFERENCES library_topics(library_id, topic_id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS video_topics_topic_idx
    ON video_topics (library_id, topic_id, score DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS video_topics_topic_idx;
DROP TABLE IF EXISTS video_topics;
DROP TABLE IF EXISTS library_topics;
-- +goose StatementEnd
