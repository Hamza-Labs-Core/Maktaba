-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0190 (Epic 56, story 56.01): play_queues and
-- play_queue_items. The shuffle order md5(video id || seed) is the API's: the store
-- registers md5() with the driver.
--
CREATE TABLE IF NOT EXISTS play_queues (
    id             TEXT    PRIMARY KEY,
    user_id        TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id      TEXT,
    source_kind    TEXT    NOT NULL
                   CHECK (source_kind IN ('library', 'series', 'season', 'collection', 'playlist', 'video')),
    source_id      TEXT    NOT NULL,
    source_filters TEXT    NOT NULL DEFAULT '{}',
    shuffle        INTEGER NOT NULL DEFAULT 0,
    seed           INTEGER,
    source_cursor  TEXT,
    source_done    INTEGER NOT NULL DEFAULT 0,
    current_pos    INTEGER NOT NULL DEFAULT 0,
    version        INTEGER NOT NULL DEFAULT 1,
    created_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    used_at        TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS play_queues_user_used_idx ON play_queues (user_id, used_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS play_queue_items (
    queue_id TEXT    NOT NULL REFERENCES play_queues(id) ON DELETE CASCADE,
    pos      INTEGER NOT NULL,
    video_id TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    origin   TEXT    NOT NULL DEFAULT 'source' CHECK (origin IN ('source', 'next', 'added')),
    PRIMARY KEY (queue_id, pos)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS play_queue_items;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS play_queues;
-- +goose StatementEnd
