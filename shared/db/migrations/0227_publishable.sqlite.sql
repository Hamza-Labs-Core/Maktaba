-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0227 (Epic 50, story 50.23): publishable sources and the
-- item marks. Broadcasting runs only against Postgres (slot 0226), so the functions that
-- read these are Postgres only; the tables and columns keep the schemas alike.
--
CREATE TABLE IF NOT EXISTS publishable_sources (
    id                TEXT     PRIMARY KEY,
    kind              TEXT     NOT NULL CHECK (kind IN ('channel', 'playlist', 'speaker', 'label')),
    ref               TEXT     NOT NULL CHECK (length(trim(ref)) > 0),
    label             TEXT     NOT NULL DEFAULT '',
    proof_url         TEXT     NOT NULL,
    proof_file        TEXT,
    note              TEXT     NOT NULL DEFAULT '',
    credit            TEXT     NOT NULL DEFAULT '',
    non_commercial    INTEGER  NOT NULL DEFAULT 0,
    recorded_by       TEXT     NOT NULL,
    recorded_by_user  TEXT     REFERENCES users(id) ON DELETE SET NULL,
    recorded_at       TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    revoked_at        TEXT,
    revoked_reason    TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS publishable_sources_standing_idx
    ON publishable_sources (kind, lower(ref)) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS publishable_reason TEXT
    CHECK (publishable_reason IS NULL OR publishable_reason IN ('own', 'cleared'));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS publishable_proof TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS publishable_by TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS publishable_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE youtube_videos ADD COLUMN IF NOT EXISTS license TEXT
    CHECK (license IS NULL OR license IN ('cc_by', 'standard'));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE broadcast_channels ADD COLUMN IF NOT EXISTS geo_mode TEXT NOT NULL DEFAULT 'allow'
    CHECK (geo_mode IN ('allow', 'deny'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE broadcast_channels DROP COLUMN geo_mode;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE youtube_videos DROP COLUMN license;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN publishable_at;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN publishable_by;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN publishable_proof;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN publishable_reason;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS publishable_sources_standing_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS publishable_sources;
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM app_settings WHERE key = 'broadcast.publishable_only';
-- +goose StatementEnd
