-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0089 (plan 06-11 section 10.3).
--
-- Same tables as the Postgres slot. SQLite has no NOTIFY, so the jobs.control
-- triggers are omitted; the Python helper publishes jobs.control on the
-- in-process bus after changing a pool, a stage or the global pause.
--
CREATE TABLE IF NOT EXISTS queue_control (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    paused     INTEGER NOT NULL DEFAULT 0,
    paused_at  TEXT,
    paused_by  TEXT,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT OR IGNORE INTO queue_control (id, paused) VALUES (1, 0);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS job_events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id        INTEGER NOT NULL,
    subject_kind  TEXT NOT NULL,
    subject_id    TEXT NOT NULL,
    library_id    TEXT,
    stage         TEXT NOT NULL,
    at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    level         TEXT NOT NULL CHECK (level IN ('debug','info','warn','error')),
    kind          TEXT NOT NULL CHECK (kind IN (
                      'enqueued','coalesced','rerun_requested','claimed',
                      'step','skipped','done','failed','retry_scheduled','cancelled','paused',
                      'resumed','reaped','triggered','requested','review','applied','imported')),
    message       TEXT NOT NULL,
    data          TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS workers (
    id            TEXT PRIMARY KEY,
    host          TEXT NOT NULL,
    stages        TEXT NOT NULL DEFAULT '[]',
    pools         TEXT NOT NULL DEFAULT '[]',
    path_map      TEXT NOT NULL DEFAULT '{}',
    version       TEXT,
    started_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS extensions (
    name        TEXT PRIMARY KEY,
    enabled     INTEGER NOT NULL DEFAULT 0,
    settings    TEXT NOT NULL DEFAULT '{}',
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS health_samples (
    at      TEXT NOT NULL,
    host    TEXT NOT NULL,
    metric  TEXT NOT NULL,
    value   REAL NOT NULL,
    PRIMARY KEY (metric, host, at)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS job_events_job_idx ON job_events (job_id, id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS job_events_stage_idx ON job_events (stage, id DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS job_events_subject_idx
    ON job_events (subject_kind, subject_id, id DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS job_events_library_idx
    ON job_events (library_id, id DESC) WHERE library_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS job_events_at_idx ON job_events (at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS health_samples_at_idx ON health_samples (at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS extensions;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS workers;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS health_samples;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS job_events;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS queue_control;
-- +goose StatementEnd
