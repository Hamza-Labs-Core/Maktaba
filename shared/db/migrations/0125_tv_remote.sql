-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Runs outside a transaction because the indexes are built CONCURRENTLY.
--
-- Slot 0125 (Epic 32, stories 32.7, 32.8 and 32.10) - remote viewing through Cloudflare
-- Stream (specs/epics/32-tv-streaming-cluster-remote/design.md section 7).
--
-- tv_remote_inputs is Maktaba's pool of Cloudflare live inputs (at most
-- tv_remote.max_channels). The stream key / SRT passphrase never live here: they are in
-- home_secrets (cf_input.<uid>); ingest keeps only the URLs.
--
-- tv_remote_pushes is one broadcast of one channel to one input: the API's remote
-- controller drives its state; the home's front door push relay (Epic 33.8) runs it and
-- reports through tv_push_report. At most one push per channel is active.
--
-- tv_remote_sessions is one remote viewer's session (user, channel, device): the tune
-- API opens it, the heartbeat keeps it, the controller ends it after 150 s of silence.
--
-- tv_remote_usage_daily is the month's cost estimate: minutes delivered per day
-- (Cloudflare analytics, or session minutes when the token cannot read analytics).
--
-- tv_push_secrets holds the ingest URL and key of a running push only (written by the
-- API when the push is requested, deleted when it ends). Only the home's own database
-- user reads it, through tv_push_credentials; the machines' group role has no right on it.
--
CREATE TABLE IF NOT EXISTS tv_remote_inputs (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    cf_uid          TEXT        NOT NULL UNIQUE,
    name            TEXT        NOT NULL,
    state           TEXT        NOT NULL DEFAULT 'idle'
                                CHECK (state IN ('idle', 'assigned', 'broadcasting', 'cleanup', 'errored')),
    channel_id      UUID        REFERENCES channels(id) ON DELETE SET NULL,
    ingest          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    last_video_uid  TEXT,
    assigned_at     TIMESTAMPTZ,
    error           TEXT,
    error_at        TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tv_remote_pushes (
    id                    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id            UUID        NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    input_id              UUID        REFERENCES tv_remote_inputs(id) ON DELETE SET NULL,
    host_id               TEXT,
    generation            BIGINT,
    state                 TEXT        NOT NULL
                                      CHECK (state IN ('requested', 'connecting', 'live', 'stopping',
                                                       'cleanup', 'done', 'failed')),
    attempt               INTEGER     NOT NULL DEFAULT 1,
    ingest                TEXT        NOT NULL DEFAULT 'rtmps' CHECK (ingest IN ('rtmps', 'srt')),
    video_uid             TEXT,
    started_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    state_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    live_at               TIMESTAMPTZ,
    idle_since            TIMESTAMPTZ,
    ended_at              TIMESTAMPTZ,
    end_reason            TEXT,
    detail                TEXT,
    relay_state           TEXT,
    relay_detail          TEXT,
    relay_at              TIMESTAMPTZ,
    minutes_delivered     NUMERIC     NOT NULL DEFAULT 0,
    recording_deleted_at  TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS tv_remote_sessions (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       TEXT        NOT NULL,
    channel_id    UUID        NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    push_id       UUID        REFERENCES tv_remote_pushes(id) ON DELETE SET NULL,
    device        TEXT        NOT NULL DEFAULT '',
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_beat_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at      TIMESTAMPTZ,
    end_reason    TEXT,
    token_exp     TIMESTAMPTZ,
    token_kid     TEXT,
    video_uid     TEXT
);

CREATE TABLE IF NOT EXISTS tv_remote_usage_daily (
    day                     DATE        PRIMARY KEY,
    minutes_delivered       NUMERIC     NOT NULL DEFAULT 0,
    peak_recording_minutes  NUMERIC     NOT NULL DEFAULT 0,
    cost_usd                NUMERIC     NOT NULL DEFAULT 0,
    source                  TEXT        NOT NULL DEFAULT 'sessions'
                                        CHECK (source IN ('analytics', 'sessions')),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tv_push_secrets (
    push_id     UUID        PRIMARY KEY REFERENCES tv_remote_pushes(id) ON DELETE CASCADE,
    url         TEXT        NOT NULL,
    key         TEXT        NOT NULL,
    stream_id   TEXT,
    expires_at  TIMESTAMPTZ NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
-- one active push per channel (story 32.7 AC6)
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS tv_remote_pushes_one_per_channel
    ON tv_remote_pushes (channel_id) WHERE state IN ('requested', 'connecting', 'live', 'stopping');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS tv_remote_pushes_state_idx
    ON tv_remote_pushes (state) WHERE state NOT IN ('done', 'failed');
-- +goose StatementEnd

-- +goose StatementBegin
-- one open session per user, channel and device: a retried tune is the same session
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS tv_remote_sessions_open_uniq
    ON tv_remote_sessions (user_id, channel_id, device) WHERE ended_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS tv_remote_sessions_push_idx
    ON tv_remote_sessions (push_id) WHERE ended_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Who may read a push's ingest key or report on it: the login that owns tv_push_secrets
-- (the home's own user: its API and its front door push relay) or a superuser. Any other
-- login (a machine's role) is refused. Fails closed.
CREATE OR REPLACE FUNCTION tv_push_guard() RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = session_user AND rolsuper)
       OR session_user = (SELECT tableowner FROM pg_tables
                           WHERE schemaname = 'public' AND tablename = 'tv_push_secrets') THEN
        RETURN true;
    END IF;
    RAISE EXCEPTION 'only the home reads push credentials'
        USING ERRCODE = 'insufficient_privilege';
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The ingest URL and key of a running push, for the home's push relay (design 7.4). Only
-- while the push is requested, connecting or live and the row has not expired.
CREATE OR REPLACE FUNCTION tv_push_credentials(p_push uuid)
RETURNS TABLE (url text, key text, stream_id text, ingest text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    PERFORM tv_push_guard();
    RETURN QUERY
        SELECT s.url, s.key, s.stream_id, p.ingest
          FROM tv_push_secrets s
          JOIN tv_remote_pushes p ON p.id = s.push_id
         WHERE s.push_id = p_push
           AND s.expires_at > now()
           AND p.state IN ('requested', 'connecting', 'live');
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The push relay reports on a push: connecting (ffmpeg up and fed), live_seen, error,
-- stopped. A requested push becomes connecting; the API's controller does the rest.
CREATE OR REPLACE FUNCTION tv_push_report(p_push uuid, p_state text, p_detail text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    PERFORM tv_push_guard();
    IF p_state NOT IN ('connecting', 'live_seen', 'error', 'stopped') THEN
        RAISE EXCEPTION 'unknown relay state %', p_state USING ERRCODE = 'invalid_parameter_value';
    END IF;
    UPDATE tv_remote_pushes
       SET relay_state = p_state,
           relay_detail = left(p_detail, 500),
           relay_at = now(),
           state = CASE WHEN p_state = 'connecting' AND state = 'requested' THEN 'connecting' ELSE state END,
           state_at = CASE WHEN p_state = 'connecting' AND state = 'requested' THEN now() ELSE state_at END
     WHERE id = p_push;
    IF FOUND THEN
        PERFORM pg_notify('tv_push_report', p_push::text || ':' || p_state);
    END IF;
    RETURN FOUND;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
REVOKE EXECUTE ON FUNCTION tv_push_guard(), tv_push_credentials(uuid),
    tv_push_report(uuid, text, text) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- the default privileges of slot 0103 gave the group role write rights on new
        -- tables: take them back. Inputs and pushes are readable (no secret columns);
        -- secrets, sessions and usage are not.
        REVOKE ALL ON tv_remote_inputs, tv_remote_pushes, tv_remote_sessions,
            tv_remote_usage_daily, tv_push_secrets FROM maktaba_worker;
        GRANT SELECT ON tv_remote_inputs, tv_remote_pushes TO maktaba_worker;
        REVOKE EXECUTE ON FUNCTION tv_push_guard() FROM maktaba_worker;
        -- callable, but the guard refuses every login but the home's
        GRANT EXECUTE ON FUNCTION tv_push_credentials(uuid), tv_push_report(uuid, text, text)
            TO maktaba_worker;
        -- slot 0103's blanket grant gave the group role write rights on goose's own
        -- table: a node must never change the home's migration history. It keeps
        -- SELECT, which the node schema guard (#84) reads.
        IF to_regclass('public.goose_db_version') IS NOT NULL THEN
            REVOKE INSERT, UPDATE, DELETE ON goose_db_version FROM maktaba_worker;
        END IF;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS tv_push_report(uuid, text, text);
DROP FUNCTION IF EXISTS tv_push_credentials(uuid);
DROP FUNCTION IF EXISTS tv_push_guard();
DROP TABLE IF EXISTS tv_push_secrets;
DROP TABLE IF EXISTS tv_remote_usage_daily;
DROP TABLE IF EXISTS tv_remote_sessions;
DROP TABLE IF EXISTS tv_remote_pushes;
DROP TABLE IF EXISTS tv_remote_inputs;
-- +goose StatementEnd
