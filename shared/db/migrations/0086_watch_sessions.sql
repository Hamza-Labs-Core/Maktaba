-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0086 (Epic 29 / Story 29.1) — watch analytics fact table.
--
-- `watch_sessions` is an APPEND log: one row per play session, opened on
-- /api/watch/start, advanced by /api/watch/heartbeat, closed by
-- /api/watch/stop (or reaped as 'interrupted' when a client vanishes).
-- It is deliberately separate from `playback_state` (slot 0038 — the
-- single resume *point* per user+video) and `streaming_sessions` (slot
-- 0039 — the transcode lifecycle): those answer "where do I resume" and
-- "what is FFmpeg doing", this answers "what was watched, by whom, when"
-- (Epic 29 README D1). `ip_addr_hash` is a salted, truncated SHA-256 of
-- the client IP — never the raw address (D4).
--
CREATE TABLE IF NOT EXISTS watch_sessions (
    id               UUID         PRIMARY KEY,
    user_id          UUID         NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    video_id         UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    started_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    ended_at         TIMESTAMPTZ,
    last_heartbeat   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    duration_sec     INTEGER      NOT NULL DEFAULT 0 CHECK (duration_sec >= 0),
    percent_complete REAL         NOT NULL DEFAULT 0 CHECK (percent_complete >= 0 AND percent_complete <= 100),
    state            TEXT         NOT NULL DEFAULT 'active'
                                  CHECK (state IN ('active','completed','stopped','interrupted')),
    device_type      TEXT,
    platform         TEXT,
    quality          TEXT,
    ip_addr_hash     TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Per-user history list, newest first (Story 29.2).
CREATE INDEX CONCURRENTLY IF NOT EXISTS watch_sessions_user_started_idx
    ON watch_sessions (user_id, started_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- Live "currently watching" view + the reaper's stale scan (Story 29.1).
-- Partial on the active set so neither pays for closed rows.
CREATE INDEX CONCURRENTLY IF NOT EXISTS watch_sessions_active_idx
    ON watch_sessions (last_heartbeat)
    WHERE state = 'active';
-- +goose StatementEnd

-- +goose StatementBegin
-- Per-video stats aggregate (Story 29.5).
CREATE INDEX CONCURRENTLY IF NOT EXISTS watch_sessions_video_idx
    ON watch_sessions (video_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Time-series / heatmap / export / retention purge scans (Stories 29.3, 29.6).
CREATE INDEX CONCURRENTLY IF NOT EXISTS watch_sessions_started_idx
    ON watch_sessions (started_at);
-- +goose StatementEnd

-- +goose StatementBegin
--
-- Per-user analytics privacy preference (Story 29.4). An ABSENT row means
-- tracking is ON (the default): the /api/watch/start handler treats a
-- missing row as track_enabled=true, so existing users are unaffected and
-- opting out is an explicit upsert.
--
CREATE TABLE IF NOT EXISTS user_analytics_prefs (
    user_id       UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    track_enabled BOOLEAN     NOT NULL DEFAULT true,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_analytics_prefs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS watch_sessions_started_idx;
DROP INDEX IF EXISTS watch_sessions_video_idx;
DROP INDEX IF EXISTS watch_sessions_active_idx;
DROP INDEX IF EXISTS watch_sessions_user_started_idx;
DROP TABLE IF EXISTS watch_sessions;
-- +goose StatementEnd
