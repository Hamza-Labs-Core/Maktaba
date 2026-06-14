-- +goose Up
-- +goose StatementBegin
-- SQLite parity sibling for slot 0079 (Story 26.7).
CREATE TABLE IF NOT EXISTS enrich_jobs (
    id          TEXT PRIMARY KEY,
    video_id    TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending','running','done','deferred','failed')),
    force       INTEGER NOT NULL DEFAULT 0,
    attempts    INTEGER NOT NULL DEFAULT 0,
    not_before  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_error  TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS library_group_pending (
    library_id TEXT PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    marked_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS enrich_jobs_open_video_idx
    ON enrich_jobs (video_id) WHERE status IN ('pending','running','deferred');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS enrich_jobs_claim_idx
    ON enrich_jobs (status, not_before) WHERE status IN ('pending','deferred');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS enrich_jobs_claim_idx;
DROP INDEX IF EXISTS enrich_jobs_open_video_idx;
DROP TABLE IF EXISTS library_group_pending;
DROP TABLE IF EXISTS enrich_jobs;
-- +goose StatementEnd
