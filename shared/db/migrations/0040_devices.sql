-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0040 (Epic 7 / Story 7.22) — registered devices for push.
--
-- One row per (user, platform, push_token). ``revoked_at`` soft-deletes
-- so the APNs/FCM bridge can lazily purge stale tokens after a delivery
-- failure.
--
CREATE TABLE IF NOT EXISTS devices (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform      TEXT         NOT NULL CHECK (platform IN ('ios','android','web')),
    push_token    TEXT         NOT NULL,
    bundle_id     TEXT         NOT NULL,
    app_version   TEXT,
    locale        TEXT,
    registered_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    revoked_at    TIMESTAMPTZ,
    UNIQUE (user_id, platform, push_token)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS devices_user_active_idx
    ON devices (user_id) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS devices_user_active_idx;
DROP TABLE IF EXISTS devices;
-- +goose StatementEnd
