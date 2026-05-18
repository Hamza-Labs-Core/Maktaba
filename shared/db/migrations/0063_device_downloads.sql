-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0063 (gap-closure Wave 3 / Epic 12 Story 12.11) —
-- device_downloads: the server-side "this video is downloaded on this
-- device" flag set, so other surfaces (web library, GraphQL
-- Video.downloads) can show where an offline copy exists.
--
-- One row per (device_id, video_id). The row is metadata-only — it
-- records that a device *claims* an offline copy; the bytes never
-- transit the server. ``revoked`` lets a soft-revoked device's rows be
-- retained for audit/UX ("last seen downloaded on <device>") rather
-- than hard-deleted when the device is unregistered.
--
-- device_id references devices(id) (slot 0040). ON DELETE CASCADE keeps
-- the set consistent if a device row is ever hard-deleted; the normal
-- path is soft-revoke (devices.revoked_at), which leaves these rows in
-- place by design.
--
CREATE TABLE IF NOT EXISTS device_downloads (
    device_id    UUID         NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    video_id     UUID         NOT NULL,
    quality      TEXT,
    size_bytes   BIGINT,
    checksum     TEXT,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    revoked      BOOLEAN      NOT NULL DEFAULT FALSE,
    PRIMARY KEY (device_id, video_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- AC: index (video_id) — "which devices have video X" reverse lookup,
-- used by the web library badge and GraphQL Video.downloads resolver.
CREATE INDEX CONCURRENTLY IF NOT EXISTS device_downloads_video_idx
    ON device_downloads (video_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS device_downloads_video_idx;
DROP TABLE IF EXISTS device_downloads;
-- +goose StatementEnd
