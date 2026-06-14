-- +goose Up
-- +goose StatementBegin
-- SQLite parity sibling for slot 0077 (Story 26.5).
CREATE TABLE IF NOT EXISTS media_metadata_enrichment (
    id          TEXT    PRIMARY KEY,
    video_id    TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    provider    TEXT    NOT NULL,
    external_id TEXT    NOT NULL,
    mapped      TEXT    NOT NULL DEFAULT '{}',
    confidence  REAL    NOT NULL DEFAULT 0,
    is_accepted INTEGER NOT NULL DEFAULT 0,
    fetched_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (video_id, provider, external_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS web_metadata_cache (
    cache_key  TEXT    PRIMARY KEY,
    provider   TEXT    NOT NULL,
    response   TEXT    NOT NULL,
    fetched_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at TEXT    NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS media_metadata_enrichment_video_idx
    ON media_metadata_enrichment (video_id, confidence DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS web_metadata_cache_expiry_idx
    ON web_metadata_cache (expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS web_metadata_cache_expiry_idx;
DROP INDEX IF EXISTS media_metadata_enrichment_video_idx;
DROP TABLE IF EXISTS web_metadata_cache;
DROP TABLE IF EXISTS media_metadata_enrichment;
-- +goose StatementEnd
