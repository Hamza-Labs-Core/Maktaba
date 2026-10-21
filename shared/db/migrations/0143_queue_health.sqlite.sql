-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0143 (Epic 40, queue health). UUIDs and timestamps are
-- TEXT, booleans 0/1, arrays and JSONB JSON text.
--
--   processing_jobs.failure_class   the class of the job's latest failure
--   processing_jobs.parked_on       set while a pending job waits on a dependency
--   dependency_health               the watchers' last probe of each dependency
--   failure_ignores                 an admin's "ignore" of a subject's failures of a stage
--   item_requests                   an admin's request on one video (redownload)
--   item_checks                     the last access check of a video's file
--   videos.missing_confirmed_at     a scan found the share up and the file still gone
--   job_stages 'access_check'       reads 64 KiB of a video's file on the worker host
--
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS failure_class TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS parked_on TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_parked_idx
    ON processing_jobs (parked_on, stage) WHERE parked_on IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS dependency_health (
    name        TEXT    PRIMARY KEY,
    healthy     INTEGER NOT NULL,
    message     TEXT,
    checked_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    since       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS failure_ignores (
    subject_kind TEXT NOT NULL,
    subject_id   TEXT NOT NULL,
    stage        TEXT NOT NULL,
    ignored_by   TEXT,
    ignored_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (subject_kind, subject_id, stage)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS item_requests (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id     TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    action       TEXT    NOT NULL CHECK (action IN ('redownload')),
    requested_by TEXT,
    requested_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    done_at      TEXT,
    result       TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS item_requests_open_idx
    ON item_requests (action, video_id) WHERE done_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS item_checks (
    video_id   TEXT    PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    ok         INTEGER NOT NULL,
    error      TEXT,
    host       TEXT,
    checked_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS missing_confirmed_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement)
VALUES
    ('access_check', 'Access check', '["video"]', 'nas_read', 'media', 13, 1, 0, 0, 1, '[]', '[]', '[]', 0, 0, 0, 'auto', 'any')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'access_check'
   AND NOT EXISTS (SELECT 1 FROM processing_jobs WHERE stage = 'access_check');
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN IF EXISTS missing_confirmed_at;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS item_checks;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS item_requests;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS failure_ignores;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS dependency_health;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS processing_jobs_parked_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP COLUMN IF EXISTS parked_on;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP COLUMN IF EXISTS failure_class;
-- +goose StatementEnd
