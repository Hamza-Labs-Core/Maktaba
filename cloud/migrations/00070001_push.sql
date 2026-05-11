-- +goose Up
-- Push notification routing. Each user device registers a token with
-- the cloud; the dispatcher walks rows here to fan out.
CREATE TABLE IF NOT EXISTS push_devices (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform    TEXT        NOT NULL CHECK (platform IN ('ios','android','web')),
    token       TEXT        NOT NULL,
    app_version TEXT,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (platform, token)
);
CREATE INDEX idx_push_devices_user ON push_devices (user_id);

CREATE TABLE IF NOT EXISTS push_dispatch_log (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL,
    platform    TEXT        NOT NULL,
    topic       TEXT,
    status      TEXT        NOT NULL,
    error       TEXT,
    sent_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_push_dispatch_user ON push_dispatch_log (user_id, sent_at DESC);

-- +goose Down
DROP TABLE IF EXISTS push_dispatch_log;
DROP TABLE IF EXISTS push_devices;
