-- +goose Up
-- Epic 58 story 58.03: the codes TVs show to sign in through Maktaba Cloud. A TV asks for a
-- code with no account; a person signed in to a home approves it there, the home claims the
-- code (server_id) and hands it a sealed payload (its connections and a single-use pairing
-- grant), and the TV collects that payload once. A code lasts 10 minutes; the api role sweeps
-- rows an hour after they expire. The poll secret is kept as its SHA-256, the payload sealed
-- (AES-GCM, bound to the code) and cleared on the poll that delivers it.
CREATE TABLE IF NOT EXISTS tv_codes (
    code               TEXT        PRIMARY KEY,
    poll_secret_hash   BYTEA       NOT NULL,
    platform           TEXT        NOT NULL DEFAULT '',
    app_version        TEXT        NOT NULL DEFAULT '',
    -- the TV's public address's country (from the edge), shown to the approver
    client_ip_country  TEXT        NOT NULL DEFAULT '',
    server_id          UUID        REFERENCES servers(id) ON DELETE CASCADE,
    payload_sealed     BYTEA,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at         TIMESTAMPTZ NOT NULL,
    claimed_at         TIMESTAMPTZ,
    delivered_at       TIMESTAMPTZ,
    -- the last poll, for slow_down
    last_poll_at       TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_tv_codes_expires ON tv_codes (expires_at);

-- +goose Down
DROP TABLE IF EXISTS tv_codes;
