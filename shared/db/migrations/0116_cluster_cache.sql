-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0116 (Epic 31, phase 2) - the database as a cache of the cluster channel
-- (specs/epics/31-multi-instance/design-agent.md section 7.9). The files on the share
-- are the record; the home's API (the hub, which runs as the owner of these tables) is
-- the only writer of the cache, and emptying it loses nothing: the hub refills it from
-- the files.
--
--   cluster_metrics_cache  one metrics frame per process per 10 s slot
--   cluster_log_cache      every log record (from the channel and from the files)
--   cluster_cache_files    how far the hub has read each file of the share, so a
--                          restart reads only what was written since
--
-- All three are UNLOGGED: fast, not in the write-ahead log, emptied together by a
-- database crash (the hub then refills the whole window). Rows are pruned past
-- MAKTABA_CLUSTER_CACHE_HOURS by the hub. Each table has a natural key, so a record read
-- twice (from the channel and from its file, or by two refills) is kept once.
--
-- health_samples stays a logged table; from this slot only the hub writes it, so the
-- machines' group role loses INSERT, UPDATE and DELETE on it (it keeps SELECT).
--
-- Runs outside a transaction because the indexes are built CONCURRENTLY.
--
CREATE UNLOGGED TABLE IF NOT EXISTS cluster_metrics_cache (
    worker_id  TEXT        NOT NULL,   -- the process's file name (<worker_id>.jsonl)
    slot       TIMESTAMPTZ NOT NULL,   -- at, down to its 10-second slot
    at         TIMESTAMPTZ NOT NULL,   -- the frame's ts
    ip         TEXT        NOT NULL,   -- the IP folder
    node_id    TEXT,                   -- NULL for the home's own processes
    frame      JSONB       NOT NULL,   -- the frame as the metrics file holds it
    PRIMARY KEY (worker_id, slot)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNLOGGED TABLE IF NOT EXISTS cluster_log_cache (
    worker_id  TEXT        NOT NULL,
    ts         TIMESTAMPTZ NOT NULL,
    h          BIGINT      NOT NULL,   -- hash of (level, event, logger, fields)
    ip         TEXT        NOT NULL,
    node_id    TEXT,
    level      TEXT        NOT NULL,
    rank       SMALLINT    NOT NULL,   -- 10 debug, 20 info, 30 warning, 40 error, 50 critical
    event      TEXT        NOT NULL,
    logger     TEXT        NOT NULL,
    fields     JSONB       NOT NULL,
    PRIMARY KEY (worker_id, ts, h)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNLOGGED TABLE IF NOT EXISTS cluster_cache_files (
    kind        TEXT        NOT NULL CHECK (kind IN ('metrics', 'logs')),
    ip          TEXT        NOT NULL,
    day         TEXT        NOT NULL,   -- YYYY-MM-DD, the day folder
    worker      TEXT        NOT NULL,   -- the file name without .jsonl
    root        TEXT        NOT NULL,   -- the cluster directory it was read in
    read_to     BIGINT      NOT NULL,   -- bytes read: every whole line before is cached
    window_s    BIGINT      NOT NULL,   -- ... that was of the window then (a longer one reads again)
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, ip, day, worker)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS cluster_metrics_cache_ip_at_idx
    ON cluster_metrics_cache (ip, at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS cluster_metrics_cache_at_idx
    ON cluster_metrics_cache (at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS cluster_log_cache_ip_ts_idx
    ON cluster_log_cache (ip, ts);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS cluster_log_cache_ip_rank_ts_idx
    ON cluster_log_cache (ip, rank, ts);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS cluster_log_cache_ts_idx
    ON cluster_log_cache (ts);
-- +goose StatementEnd

-- +goose StatementBegin
-- The machines' group (slot 0103) gets data rights on every new table through its
-- default privileges: none on the cache, and no more writing health_samples. Guarded
-- like slot 0103: the role exists only when the migrating user could create it.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        REVOKE ALL ON cluster_metrics_cache, cluster_log_cache, cluster_cache_files
            FROM maktaba_worker;
        REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON health_samples FROM maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        GRANT INSERT, UPDATE, DELETE ON health_samples TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_cache_files;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_log_cache;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_metrics_cache;
-- +goose StatementEnd
