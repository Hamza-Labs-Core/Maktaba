-- +goose Up
-- Story 50.16: a pool of relays.
--
-- relays:                 the pool's members, one row per relay machine ("r1", "r2", …), each
--                         heartbeat every 5 s by the relay processes running on it (both of a
--                         blue/green pair write the same row). Healthy: state 'active' and a
--                         heartbeat under 15 s old. 'draining' and 'down' are set by a deploy or
--                         by staff (`maktaba-cloud admin relay-state`), never by a heartbeat.
--                         capacity_mbps (from 50.14's measurement) weights the rendezvous hash.
-- relay_standby_tunnels:  a home's idle standby tunnel (its second, on the relay ranked second
--                         for it). relay_tunnels (50.04) keeps the primary tunnels with its
--                         primary key on slug: the release running beside this one during a
--                         blue/green deploy upserts there ON CONFLICT (slug), so the standby rows
--                         live in their own table rather than as a role column under a wider key.
CREATE TABLE IF NOT EXISTS relays (
    id            TEXT        PRIMARY KEY,
    public_ip     INET        NOT NULL,
    private_ip    INET        NOT NULL,
    state         TEXT        NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'draining', 'down')),
    heartbeat_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    capacity_mbps INTEGER     NOT NULL CHECK (capacity_mbps > 0)
);

CREATE TABLE IF NOT EXISTS relay_standby_tunnels (
    slug         TEXT        PRIMARY KEY,
    server_id    UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    relay_id     TEXT        NOT NULL,
    relay_addr   TEXT        NOT NULL,
    seq          BIGINT      NOT NULL,
    connected_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS relay_standby_tunnels_relay_id ON relay_standby_tunnels (relay_id);

-- +goose Down
DROP TABLE IF EXISTS relay_standby_tunnels;
DROP TABLE IF EXISTS relays;
