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
    attempts    INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    reviewed_by TEXT    REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at TEXT,
    updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK (not_checked IS NULL OR tier IS NULL)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- a database that ran this slot before the column was in it
ALTER TABLE video_content_signals
    ADD COLUMN IF NOT EXISTS attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS video_content_signals_review_idx
    ON video_content_signals (updated_at DESC, video_id)
    WHERE reviewed_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- the content_screen stage (Epic 57, story 57.17): video and library subjects, the cpu
-- pool, speech lane 10, on the home only; its edges are the pipeline's to write (its graph
-- sync). The Down leaves the row, as every stage seed does: the queue's rows name it.
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled, disabled_reason)
VALUES
    ('content_screen', 'Content screen', '["video","library"]', 'cpu', 'speech', 10, 1, 0, 0, 3, '[]', '[]', '[]', 0, 0, 0, 'auto', 'home', 1, NULL)
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS video_content_signals;
-- +goose StatementEnd
