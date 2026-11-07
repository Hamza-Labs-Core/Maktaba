-- +goose Up
-- Story 50.15: direct remote access. Each home's certificate (its direct-name domain and CA stay
-- with it), the outside check's last result, and the ACME DNS-01 challenges the responder serves.
-- The home makes its key; only its CSR and the public chain are kept here, never a private key.
CREATE TABLE IF NOT EXISTS direct_certs (
    server_id    UUID        PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    home_id      TEXT        NOT NULL UNIQUE,
    domain       TEXT        NOT NULL,
    ca           TEXT        NOT NULL,
    csr          BYTEA,                 -- a CSR waiting for the worker (DER); null once issued
    cert_pem     TEXT,                  -- the chain issued (public)
    issued_at    TIMESTAMPTZ,           -- the first certificate: counts against the domain's week
    renewed_at   TIMESTAMPTZ,
    not_after    TIMESTAMPTZ,
    error        TEXT,
    delivered_at TIMESTAMPTZ,           -- when a relay last handed the chain to the home
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS direct_certs_domain_issued ON direct_certs (domain, issued_at);

CREATE TABLE IF NOT EXISTS direct_checks (
    server_id    UUID        PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    result       TEXT,                  -- ok, closed, timeout, wrong_home, cgnat
    port         INTEGER,
    checked_at   TIMESTAMPTZ,
    requested_at TIMESTAMPTZ,           -- the admin pressed Check now
    delivered_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS acme_challenges (
    name       TEXT        NOT NULL,
    value      TEXT        NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (name, value)
);

-- what the home last reported of its direct listener: on, off, cgnat, mapping_failed
ALTER TABLE servers ADD COLUMN IF NOT EXISTS direct_state TEXT;

-- +goose Down
ALTER TABLE servers DROP COLUMN IF EXISTS direct_state;
DROP TABLE IF EXISTS acme_challenges;
DROP TABLE IF EXISTS direct_checks;
DROP TABLE IF EXISTS direct_certs;
