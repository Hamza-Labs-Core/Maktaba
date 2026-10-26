-- +goose NO TRANSACTION
-- +goose Up
--
-- Slot 0158 (Epic 44, story 44.1) - analytics counts every way of watching, and every
-- app's use of the API.
--
--   watch_sessions        gains stream_session_id (a play recorded server-side from the
--                         stream session every player opens: one row per stream session),
--                         client_app and client_version (from X-Maktaba-Client)
--   tv_watch_sessions     one row per viewing of a channel: in a Maktaba app or the web
--                         (surface 'app'), through Plex's Live TV and the HDHomeRun tuner
--                         ('tuner'; no person, the viewer's address hashed), or the remote
--                         TV ('remote', which keeps tv_remote_sessions; analytics reads that)
--   api_usage_daily       the API's requests per day, app and platform (errors, time, slow)
--   api_usage_people_daily who used which app on a day
--
-- maktaba_worker keeps the default rights (slot 0103) on the new tables, as on every
-- ordinary table (slot 0152's lesson). The rows hold no secret. NO TRANSACTION for the
-- indexes (CONCURRENTLY), as slot 0150.

ALTER TABLE watch_sessions ADD COLUMN IF NOT EXISTS stream_session_id UUID;
ALTER TABLE watch_sessions ADD COLUMN IF NOT EXISTS client_app TEXT;
ALTER TABLE watch_sessions ADD COLUMN IF NOT EXISTS client_version TEXT;

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS watch_sessions_stream_session_idx
    ON watch_sessions (stream_session_id) WHERE stream_session_id IS NOT NULL;

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS tv_watch_sessions (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id       UUID        NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id          UUID        REFERENCES users(id) ON DELETE CASCADE,
    surface          TEXT        NOT NULL CHECK (surface IN ('app', 'tuner', 'remote')),
    client_app       TEXT,
    client_version   TEXT,
    platform         TEXT,
    device_type      TEXT,
    client_addr_hash TEXT,
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at         TIMESTAMPTZ,
    seconds          INTEGER     NOT NULL DEFAULT 0 CHECK (seconds >= 0),
    end_reason       TEXT        CHECK (end_reason IN ('closed', 'idle', 'replaced'))
);
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS tv_watch_sessions_started_idx
    ON tv_watch_sessions (started_at);

CREATE INDEX CONCURRENTLY IF NOT EXISTS tv_watch_sessions_channel_idx
    ON tv_watch_sessions (channel_id, started_at);

CREATE INDEX CONCURRENTLY IF NOT EXISTS tv_watch_sessions_open_idx
    ON tv_watch_sessions (last_seen_at) WHERE ended_at IS NULL;

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS api_usage_daily (
    day        DATE    NOT NULL,
    client_app TEXT    NOT NULL,
    platform   TEXT    NOT NULL,
    requests   BIGINT  NOT NULL DEFAULT 0 CHECK (requests >= 0),
    errors     BIGINT  NOT NULL DEFAULT 0 CHECK (errors >= 0),
    total_ms   BIGINT  NOT NULL DEFAULT 0 CHECK (total_ms >= 0),
    slow       BIGINT  NOT NULL DEFAULT 0 CHECK (slow >= 0),
    PRIMARY KEY (day, client_app, platform)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS api_usage_people_daily (
    day            DATE NOT NULL,
    client_app     TEXT NOT NULL,
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_version TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (day, client_app, user_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS api_usage_people_daily;
DROP TABLE IF EXISTS api_usage_daily;
DROP INDEX IF EXISTS tv_watch_sessions_open_idx;
DROP INDEX IF EXISTS tv_watch_sessions_channel_idx;
DROP INDEX IF EXISTS tv_watch_sessions_started_idx;
DROP TABLE IF EXISTS tv_watch_sessions;
DROP INDEX IF EXISTS watch_sessions_stream_session_idx;
ALTER TABLE watch_sessions DROP COLUMN IF EXISTS client_version;
ALTER TABLE watch_sessions DROP COLUMN IF EXISTS client_app;
ALTER TABLE watch_sessions DROP COLUMN IF EXISTS stream_session_id;
-- +goose StatementEnd
