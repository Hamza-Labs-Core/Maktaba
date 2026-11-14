-- +goose Up
-- Story 50.23 §5: Maktaba's own country deny list for broadcasting. A service admin sets it
-- (/v1/admin/broadcast/geo-deny, audited); the cloud merges it into the deny list of every
-- ingest token it signs, so the edge refuses those countries on every channel (451
-- geo_blocked) and a home cannot leave it out. It starts empty: the countries are a legal
-- decision made by the service admins.
CREATE TABLE IF NOT EXISTS broadcast_geo_deny (
    country   text        PRIMARY KEY CHECK (country ~ '^[A-Z]{2}$'),
    reason    text        NOT NULL DEFAULT '',
    added_by  text        NOT NULL DEFAULT '',
    added_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS broadcast_geo_deny;
