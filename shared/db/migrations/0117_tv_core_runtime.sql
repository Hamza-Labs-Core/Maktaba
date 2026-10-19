-- +goose Up
-- +goose StatementBegin
--
-- Slot 0117 (Epic 27, TV core) - where each channel is encoding, cluster-wide, and a tuner
-- sized for a real lineup.
--
-- channel_runtime becomes the cluster-wide record of a channel on air (one encode per
-- channel in the whole cluster):
--   host_id          the streaming host's stable id (a joined node's node_id; the home's
--                    instance id); `host` stays its host name
--   stream_base_url  where that host's streaming port is reached (the home proxies or
--                    redirects a viewer there)
--   encoder          the encoder of the block on air: nvenc | videotoolbox | software
--   restarts         muxer restarts
--   nvenc_fallbacks  blocks encoded by libx264 instead of the hardware encoder (the session
--                    cap was reached or a session was refused)
--   heartbeat_at     rewritten every ~10 s while the station runs; a row whose heartbeat is
--                    older than ~45 s is stale (its host died) and no longer owns the channel
-- A streaming host clears its own rows when it starts (stations live in memory).
--
-- streaming_hosts is each streaming host's capacity, heartbeated with its rows, so the home
-- can pick a host for a channel: its role (home | node), how many channels it may keep on
-- air and has on air, its encoder, and the hardware-encoder slots free (-1: no cap).
--
-- hdhr_device.tuner_count defaults to 20 (was 4): a tuner is one Plex/IPTV client pull and
-- clients of the same channel share its one encode, so it is independent of how many
-- channels a host encodes at once. Existing rows keep their count.
--
ALTER TABLE channel_runtime ADD COLUMN IF NOT EXISTS host_id TEXT;
ALTER TABLE channel_runtime ADD COLUMN IF NOT EXISTS stream_base_url TEXT;
ALTER TABLE channel_runtime ADD COLUMN IF NOT EXISTS encoder TEXT;
ALTER TABLE channel_runtime ADD COLUMN IF NOT EXISTS restarts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channel_runtime ADD COLUMN IF NOT EXISTS nvenc_fallbacks INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channel_runtime ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS streaming_hosts (
    host_id          TEXT        PRIMARY KEY,
    host             TEXT        NOT NULL,
    role             TEXT        NOT NULL DEFAULT 'home' CHECK (role IN ('home','node')),
    stream_base_url  TEXT,
    encoder          TEXT,
    max_active       INTEGER     NOT NULL DEFAULT 0,
    active_channels  INTEGER     NOT NULL DEFAULT 0,
    nvenc_free       INTEGER     NOT NULL DEFAULT 0,
    vt_free          INTEGER     NOT NULL DEFAULT 0,
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    heartbeat_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE hdhr_device ALTER COLUMN tuner_count SET DEFAULT 20;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE hdhr_device ALTER COLUMN tuner_count SET DEFAULT 4;
DROP TABLE IF EXISTS streaming_hosts;
ALTER TABLE channel_runtime DROP COLUMN IF EXISTS heartbeat_at;
ALTER TABLE channel_runtime DROP COLUMN IF EXISTS nvenc_fallbacks;
ALTER TABLE channel_runtime DROP COLUMN IF EXISTS restarts;
ALTER TABLE channel_runtime DROP COLUMN IF EXISTS encoder;
ALTER TABLE channel_runtime DROP COLUMN IF EXISTS stream_base_url;
ALTER TABLE channel_runtime DROP COLUMN IF EXISTS host_id;
-- +goose StatementEnd
