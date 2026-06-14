-- +goose Up
-- +goose StatementBegin
-- SQLite parity sibling for slot 0078 (Story 26.6).
CREATE TABLE IF NOT EXISTS media_field_provenance (
    video_id   TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    field      TEXT NOT NULL,
    origin     TEXT NOT NULL CHECK (origin IN ('user','enrichment','parser')),
    prev_value TEXT,
    source_id  TEXT,
    set_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (video_id, field)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS enrichment_decisions (
    id            TEXT PRIMARY KEY,
    video_id      TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    external_id   TEXT,
    action        TEXT NOT NULL CHECK (action IN ('accept','dismiss','revert','auto_accept')),
    actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    applied       TEXT NOT NULL DEFAULT '[]',
    skipped       TEXT NOT NULL DEFAULT '[]',
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE media_metadata_enrichment ADD COLUMN is_dismissed INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS enrichment_decisions_video_idx
    ON enrichment_decisions (video_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS enrichment_decisions_video_idx;
ALTER TABLE media_metadata_enrichment DROP COLUMN is_dismissed;
DROP TABLE IF EXISTS enrichment_decisions;
DROP TABLE IF EXISTS media_field_provenance;
-- +goose StatementEnd
