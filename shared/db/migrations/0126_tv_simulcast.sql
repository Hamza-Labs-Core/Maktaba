-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Runs outside a transaction because the indexes are built CONCURRENTLY.
--
-- Slot 0126 (Epic 32, story 32.13) - simulcast a channel to YouTube, Facebook or any
-- RTMP/RTMPS destination through Cloudflare live outputs on the channel's push input
-- (specs/epics/32-tv-streaming-cluster-remote/design.md section 7.9).
--
-- tv_simulcast_destinations is a destination the admin defined: name, kind, ingest URL.
-- Its stream key is never here: it is in home_secrets (simulcast_<id>_key, slot 0120),
-- sent to Cloudflare only when an output is created.
--
-- tv_simulcast_attachments is a channel x destination: always, or on a schedule (rules:
-- weekly windows, while a programme / series airs, during live slots), with the admin's
-- rights confirmation (who, when) and a manual start/stop override until the next window
-- boundary.
--
-- tv_simulcast_outputs is one output's life: created on the push's input when a window
-- opens, deleted at Cloudflare when it closes. At most one open row (ended_at NULL) per
-- attachment. input_uid keeps the Cloudflare input id, so an output can be deleted even
-- after its rows lost their attachment (a channel or attachment deleted).
--
-- tv_remote_pushes.holds lists what keeps a push up besides the idle timeout: viewers,
-- simulcast. tv_remote_usage_daily.simulcast_minutes is the day's output minutes (they
-- count as minutes delivered).
--
CREATE TABLE IF NOT EXISTS tv_simulcast_destinations (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    kind        TEXT        NOT NULL CHECK (kind IN ('youtube', 'facebook', 'rtmp')),
    ingest_url  TEXT        NOT NULL,
    key_secret  TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tv_simulcast_attachments (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id           UUID        NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    destination_id       UUID        NOT NULL REFERENCES tv_simulcast_destinations(id) ON DELETE CASCADE,
    mode                 TEXT        NOT NULL CHECK (mode IN ('always', 'schedule')),
    rules                JSONB       NOT NULL DEFAULT '[]'::jsonb,
    enabled              BOOLEAN     NOT NULL DEFAULT false,
    manual               TEXT        CHECK (manual IN ('start', 'stop')),
    manual_until         TIMESTAMPTZ,
    rights_confirmed_by  TEXT,
    rights_confirmed_at  TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (channel_id, destination_id)
);

CREATE TABLE IF NOT EXISTS tv_simulcast_outputs (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    attachment_id   UUID        REFERENCES tv_simulcast_attachments(id) ON DELETE SET NULL,
    channel_id      UUID        REFERENCES channels(id) ON DELETE SET NULL,
    destination_id  UUID        REFERENCES tv_simulcast_destinations(id) ON DELETE SET NULL,
    push_id         UUID        REFERENCES tv_remote_pushes(id) ON DELETE SET NULL,
    input_id        UUID        REFERENCES tv_remote_inputs(id) ON DELETE SET NULL,
    input_uid       TEXT,
    cf_output_uid   TEXT,
    state           TEXT        NOT NULL
                                CHECK (state IN ('scheduled', 'starting', 'live', 'stopping', 'off', 'errored')),
    window_start    TIMESTAMPTZ,
    window_end      TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    minutes         NUMERIC     NOT NULL DEFAULT 0,
    detail          TEXT,
    error           TEXT,
    cf_status       TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE tv_remote_pushes ADD COLUMN IF NOT EXISTS holds JSONB NOT NULL DEFAULT '[]'::jsonb;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE tv_remote_usage_daily ADD COLUMN IF NOT EXISTS simulcast_minutes NUMERIC NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
-- one open output per attachment
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS tv_simulcast_outputs_open_uniq
    ON tv_simulcast_outputs (attachment_id) WHERE ended_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS tv_simulcast_outputs_input_idx
    ON tv_simulcast_outputs (input_id) WHERE ended_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- slot 0103's default privileges gave the group role rights on new tables: a
        -- machine has nothing to do with simulcast (the home API is the only reader).
        REVOKE ALL ON tv_simulcast_destinations, tv_simulcast_attachments, tv_simulcast_outputs
            FROM maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tv_simulcast_outputs;
DROP TABLE IF EXISTS tv_simulcast_attachments;
DROP TABLE IF EXISTS tv_simulcast_destinations;
ALTER TABLE tv_remote_usage_daily DROP COLUMN IF EXISTS simulcast_minutes;
ALTER TABLE tv_remote_pushes DROP COLUMN IF EXISTS holds;
-- +goose StatementEnd
