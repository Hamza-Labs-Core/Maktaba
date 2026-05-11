-- +goose Up
-- Entitlement signing: keys we rotate quarterly and the audit log of
-- grants we've issued. The keypair private bytes do NOT live in the
-- database — only the public key + fingerprint do, so a DB compromise
-- cannot forge new entitlements.
CREATE TABLE IF NOT EXISTS entitlement_keys (
    fingerprint TEXT        PRIMARY KEY,
    public_key  BYTEA       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ,
    active      BOOLEAN     NOT NULL DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS entitlement_grants (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    server_id     UUID        REFERENCES servers(id) ON DELETE SET NULL,
    plan          TEXT        NOT NULL,
    issued_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    fingerprint   TEXT        NOT NULL REFERENCES entitlement_keys(fingerprint),
    revoked_at    TIMESTAMPTZ
);
CREATE INDEX idx_entitlement_grants_user ON entitlement_grants (user_id);

-- +goose Down
DROP TABLE IF EXISTS entitlement_grants;
DROP TABLE IF EXISTS entitlement_keys;
