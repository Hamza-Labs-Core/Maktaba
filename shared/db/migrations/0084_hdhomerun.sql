-- +goose Up
-- +goose StatementBegin
--
-- Slot 0084 (Epic 27 / Story 27.5) — emulated HDHomeRun device + tuner
-- leases.
--
-- `hdhr_device` is a singleton (CHECK id = 1): one virtual tuner device
-- advertised on the LAN so Plex/Jellyfin/Emby can discover Maktaba's
-- channels with zero config. `device_id` is generated once and persisted
-- (D3) because Plex binds its DVR to it; it must survive restarts.
-- `enabled` defaults false (D7) — the feature is strictly opt-in, no
-- surprise SSDP advertisement.
--
-- `hdhr_tuner_leases` caps concurrent external pulls at `tuner_count`
-- (D5): one lease == one engine MPEG-TS consumer, released on disconnect.
--
CREATE TABLE IF NOT EXISTS hdhr_device (
    id            INTEGER     PRIMARY KEY DEFAULT 1 CHECK (id = 1),  -- singleton
    device_id     TEXT        NOT NULL,                  -- stable, generated once (D3)
    device_uuid   UUID        NOT NULL DEFAULT gen_random_uuid(),    -- UPnP UDN
    friendly_name TEXT        NOT NULL DEFAULT 'Maktaba',
    tuner_count   INTEGER     NOT NULL DEFAULT 4,        -- validated <= host cap (D8)
    enabled       BOOLEAN     NOT NULL DEFAULT false,    -- opt-in (D7)
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hdhr_tuner_leases (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id  UUID        NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    client_addr TEXT        NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS hdhr_tuner_leases_active_idx ON hdhr_tuner_leases (last_seen);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS hdhr_tuner_leases_active_idx;
DROP TABLE IF EXISTS hdhr_tuner_leases;
DROP TABLE IF EXISTS hdhr_device;
-- +goose StatementEnd
