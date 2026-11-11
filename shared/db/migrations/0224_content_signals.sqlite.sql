-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0224 (Epic 57, story 57.17): what the content_screen
-- stage detected for a video. SQLite has no roles and no recompute (the API's, which is
-- the one writer of videos.age_tier and its source, runs on Postgres), so there is no
-- trigger here and the check on videos.age_tier_source is left as slot 0220 made it:
-- nothing on SQLite ever writes 'detected'.
--
CREATE TABLE IF NOT EXISTS video_content_signals (
    video_id    TEXT    PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    tier        INTEGER CHECK (tier IS NULL OR tier IN (0, 9, 13, 16, 18)),
    confidence  REAL    NOT NULL DEFAULT 0 CHECK (confidence >= 0 AND confidence <= 1),
    categories  TEXT    NOT NULL DEFAULT '{}',
    not_checked TEXT    CHECK (not_checked IS NULL
                               OR not_checked IN ('no_transcript', 'unsupported_language')),
    model       TEXT    NOT NULL DEFAULT '',
    input_hash  TEXT    NOT NULL DEFAULT '',
    reviewed_by TEXT    REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at TEXT,
    updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK (not_checked IS NULL OR tier IS NULL)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS video_content_signals_review_idx
    ON video_content_signals (updated_at DESC, video_id)
    WHERE reviewed_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS video_content_signals;
-- +goose StatementEnd
