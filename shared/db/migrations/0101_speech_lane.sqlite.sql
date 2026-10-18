-- +goose Up
--
-- SQLite parity sibling for slot 0101 (plan 03-10): the 'speech' lane. SQLite cannot
-- swap a CHECK in place, so job_stages is rebuilt with the create-new / copy /
-- drop-old / rename sequence (precedent: slots 0063 and 0088), with the column set of
-- slot 0087 (no later slot adds a column) and an explicit column list.
--
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS job_stages_0101_new (
    stage            TEXT PRIMARY KEY CHECK (length(stage) BETWEEN 2 AND 40),
    label            TEXT NOT NULL,
    subject_kinds    TEXT NOT NULL,
    pool             TEXT NOT NULL REFERENCES worker_pools(pool),
    lane             TEXT NOT NULL CHECK (lane IN ('sources','episodes','speech','seasons','channels','media')),
    lane_order       INTEGER NOT NULL DEFAULT 0,
    version          INTEGER NOT NULL DEFAULT 1,
    debounce_sec     INTEGER NOT NULL DEFAULT 0,
    debounce_max_sec INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER NOT NULL DEFAULT 3,
    edges            TEXT NOT NULL DEFAULT '[]',
    may_request      TEXT NOT NULL DEFAULT '[]',
    entry_for        TEXT NOT NULL DEFAULT '[]',
    legacy           INTEGER NOT NULL DEFAULT 0,
    hidden           INTEGER NOT NULL DEFAULT 0,
    external_write   INTEGER NOT NULL DEFAULT 0,
    extension        TEXT,
    enabled          INTEGER NOT NULL DEFAULT 1,
    disabled_reason  TEXT,
    paused           INTEGER NOT NULL DEFAULT 0,
    paused_at        TEXT,
    paused_by        TEXT,
    write_mode       TEXT NOT NULL DEFAULT 'auto' CHECK (write_mode IN ('auto','review')),
    graph_synced_at  TEXT,
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT OR IGNORE INTO job_stages_0101_new (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec, debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden, external_write, extension, enabled, disabled_reason, paused, paused_at, paused_by, write_mode, graph_synced_at, updated_at)
SELECT stage, label, subject_kinds, pool, CASE WHEN stage IN ('transcribe','subtitle_gen','index') THEN 'speech' ELSE lane END, lane_order, version, debounce_sec, debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden, external_write, extension, enabled, disabled_reason, paused, paused_at, paused_by, write_mode, graph_synced_at, updated_at FROM job_stages;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS job_stages;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE job_stages_0101_new RENAME TO job_stages;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS job_stages_0101_new (
    stage            TEXT PRIMARY KEY CHECK (length(stage) BETWEEN 2 AND 40),
    label            TEXT NOT NULL,
    subject_kinds    TEXT NOT NULL,
    pool             TEXT NOT NULL REFERENCES worker_pools(pool),
    lane             TEXT NOT NULL CHECK (lane IN ('sources','episodes','seasons','channels','media')),
    lane_order       INTEGER NOT NULL DEFAULT 0,
    version          INTEGER NOT NULL DEFAULT 1,
    debounce_sec     INTEGER NOT NULL DEFAULT 0,
    debounce_max_sec INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER NOT NULL DEFAULT 3,
    edges            TEXT NOT NULL DEFAULT '[]',
    may_request      TEXT NOT NULL DEFAULT '[]',
    entry_for        TEXT NOT NULL DEFAULT '[]',
    legacy           INTEGER NOT NULL DEFAULT 0,
    hidden           INTEGER NOT NULL DEFAULT 0,
    external_write   INTEGER NOT NULL DEFAULT 0,
    extension        TEXT,
    enabled          INTEGER NOT NULL DEFAULT 1,
    disabled_reason  TEXT,
    paused           INTEGER NOT NULL DEFAULT 0,
    paused_at        TEXT,
    paused_by        TEXT,
    write_mode       TEXT NOT NULL DEFAULT 'auto' CHECK (write_mode IN ('auto','review')),
    graph_synced_at  TEXT,
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT OR IGNORE INTO job_stages_0101_new (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec, debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden, external_write, extension, enabled, disabled_reason, paused, paused_at, paused_by, write_mode, graph_synced_at, updated_at)
SELECT stage, label, subject_kinds, pool, CASE WHEN lane = 'speech' THEN 'media' ELSE lane END, lane_order, version, debounce_sec, debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden, external_write, extension, enabled, disabled_reason, paused, paused_at, paused_by, write_mode, graph_synced_at, updated_at FROM job_stages;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS job_stages;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE job_stages_0101_new RENAME TO job_stages;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA foreign_keys = ON;
-- +goose StatementEnd
