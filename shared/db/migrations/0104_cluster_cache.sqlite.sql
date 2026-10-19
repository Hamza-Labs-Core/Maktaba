-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0104 (Epic 31, phase 2): the database cache of the
-- cluster channel. The same tables, columns and keys as the Postgres slot. SQLite has
-- no UNLOGGED tables (these are plain tables; emptying them loses nothing, the hub
-- refills them from the files) and no roles (a SQLite home takes no machines).
-- Timestamps are ISO-8601 UTC text, fixed width, so they sort as times.
--
CREATE TABLE IF NOT EXISTS cluster_metrics_cache (
    worker_id  TEXT NOT NULL,
    slot       TEXT NOT NULL,
    at         TEXT NOT NULL,
    ip         TEXT NOT NULL,
    node_id    TEXT,
    frame      TEXT NOT NULL,
    PRIMARY KEY (worker_id, slot)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cluster_log_cache (
    worker_id  TEXT    NOT NULL,
    ts         TEXT    NOT NULL,
    h          INTEGER NOT NULL,
    ip         TEXT    NOT NULL,
    node_id    TEXT,
    level      TEXT    NOT NULL,
    rank       INTEGER NOT NULL,
    event      TEXT    NOT NULL,
    logger     TEXT    NOT NULL,
    fields     TEXT    NOT NULL,
    PRIMARY KEY (worker_id, ts, h)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cluster_cache_files (
    kind        TEXT    NOT NULL CHECK (kind IN ('metrics', 'logs')),
    ip          TEXT    NOT NULL,
    day         TEXT    NOT NULL,
    worker      TEXT    NOT NULL,
    root        TEXT    NOT NULL,
    read_to     INTEGER NOT NULL,
    window_s    INTEGER NOT NULL,
    updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (kind, ip, day, worker)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS cluster_metrics_cache_ip_at_idx ON cluster_metrics_cache (ip, at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS cluster_metrics_cache_at_idx ON cluster_metrics_cache (at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS cluster_log_cache_ip_ts_idx ON cluster_log_cache (ip, ts);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS cluster_log_cache_ip_rank_ts_idx ON cluster_log_cache (ip, rank, ts);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS cluster_log_cache_ts_idx ON cluster_log_cache (ts);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_cache_files;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_log_cache;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_metrics_cache;
-- +goose StatementEnd
