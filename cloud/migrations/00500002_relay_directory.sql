-- +goose Up
-- Story 50.04: the tunnel directory. Which relay process holds each home's
-- tunnel (relay_tunnels), and the relay processes alive (relay_nodes, a
-- heartbeat every 5 s; one silent for 20 s is dead and its rows are swept).
-- A relay without a home's tunnel forwards the request to the one that has it.
CREATE TABLE IF NOT EXISTS relay_nodes (
    relay_id     TEXT        PRIMARY KEY,
    relay_addr   TEXT        NOT NULL,
    heartbeat_at TIMESTAMPTZ NOT NULL,
    draining     BOOLEAN     NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS relay_tunnels (
    slug         TEXT        PRIMARY KEY,
    server_id    UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    relay_id     TEXT        NOT NULL,
    relay_addr   TEXT        NOT NULL,
    seq          BIGINT      NOT NULL,
    connected_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS relay_tunnels_relay_id ON relay_tunnels (relay_id);

-- +goose Down
DROP TABLE IF EXISTS relay_tunnels;
DROP TABLE IF EXISTS relay_nodes;
