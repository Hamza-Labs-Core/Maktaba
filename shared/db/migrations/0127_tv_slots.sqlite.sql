-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0127 (Epic 33, story 33.1): the TV slots. A SQLite home
-- is a single machine: no roles, no heartbeat function (the slot writes its row).
-- UUID becomes TEXT, JSONB TEXT, TIMESTAMPTZ TEXT.
--
CREATE TABLE IF NOT EXISTS tv_slots (
    host_id        TEXT    NOT NULL,
    slot           TEXT    NOT NULL CHECK (slot IN ('blue', 'green', 'amber')),
    role           TEXT    NOT NULL DEFAULT 'idle'
                           CHECK (role IN ('idle', 'staging', 'live', 'standby', 'previous', 'draining')),
    role_epoch     INTEGER NOT NULL DEFAULT 0,
    state          TEXT    NOT NULL DEFAULT 'stopped'
                           CHECK (state IN ('stopped', 'starting', 'healthy', 'unhealthy', 'crashed')),
    release_id     TEXT,
    base_url       TEXT,
    pid            INTEGER,
    version        TEXT,
    warm           TEXT    NOT NULL DEFAULT '[]',
    encoders_used  INTEGER NOT NULL DEFAULT 0,
    started_at     TEXT,
    beat_at        TEXT,
    last_exit      TEXT,
    PRIMARY KEY (host_id, slot)
);
CREATE UNIQUE INDEX IF NOT EXISTS tv_slots_one_live ON tv_slots (host_id) WHERE role = 'live';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS tv_slots_one_live;
DROP TABLE IF EXISTS tv_slots;
-- +goose StatementEnd
