-- +goose Up
-- Per-user Maktaba servers and the claim flow used to link them.
--
-- servers:        one row per registered on-prem server. `slug` is the
--                 subdomain prefix (e.g. "skylark-12" → skylark-12.relay.maktaba.app).
-- server_claims:  short-lived (10 min) 8-char tokens minted by the
--                 cloud and entered on the server UI to bind the two.
-- server_health:  most recent heartbeat snapshot — overwritten on each
--                 update so the table stays small.
CREATE TABLE IF NOT EXISTS servers (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT        NOT NULL,
    slug            TEXT        NOT NULL UNIQUE,
    server_secret_hash TEXT     NOT NULL,
    plan            TEXT        NOT NULL DEFAULT 'free',
    version         TEXT,
    public_key      BYTEA,
    last_seen_at    TIMESTAMPTZ,
    direct_ip       INET,
    direct_port     INTEGER,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_servers_owner ON servers (owner_user_id);

CREATE TABLE IF NOT EXISTS server_claims (
    token_hash      TEXT        PRIMARY KEY,
    code            TEXT        NOT NULL,
    user_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at      TIMESTAMPTZ NOT NULL,
    used_at         TIMESTAMPTZ,
    used_server_id  UUID        REFERENCES servers(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_server_claims_expires ON server_claims (expires_at);
CREATE INDEX idx_server_claims_code ON server_claims (code);

CREATE TABLE IF NOT EXISTS server_health (
    server_id       UUID        PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    online          BOOLEAN     NOT NULL DEFAULT FALSE,
    last_heartbeat  TIMESTAMPTZ,
    relay_latency_ms INTEGER,
    direct_latency_ms INTEGER,
    cpu_pct         REAL,
    mem_pct         REAL,
    storage_pct     REAL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS server_health;
DROP TABLE IF EXISTS server_claims;
DROP TABLE IF EXISTS servers;
