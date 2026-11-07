-- +goose Up
-- Story 50.19 §3: each home's open relayed streams (HLS sessions), so the relay limits how many
-- play at once on its plan. Written when a stream opens, touched each minute while its segments
-- flow, and deleted by the worker after 2 minutes of silence. In Postgres so the blue and green
-- relay processes of a deploy (50.04) agree; 50.16 reuses it across the pool.
CREATE TABLE IF NOT EXISTS relay_streams (
    server_id    UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    session      TEXT        NOT NULL,
    relay        TEXT        NOT NULL DEFAULT '',
    opened_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, session)
);
CREATE INDEX IF NOT EXISTS relay_streams_last_seen ON relay_streams (last_seen_at);

-- +goose Down
DROP TABLE IF EXISTS relay_streams;
