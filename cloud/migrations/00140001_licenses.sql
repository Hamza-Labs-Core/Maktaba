-- +goose Up
-- Epic 16 story 16.4: the licenses the cloud issued and the ones it
-- revoked. Licenses themselves are signed and live on the homes; these
-- rows are the operator's record and the source of the signed
-- revocation list homes fetch daily.
--
-- licenses_issued: one row per license id, the latest issue (a home's
-- plan license is re-issued daily under the same id, cloud-<server id>;
-- an offline license is issued once from the CLI).
CREATE TABLE IF NOT EXISTS licenses_issued (
    license_id   TEXT        PRIMARY KEY,
    kind         TEXT        NOT NULL CHECK (kind IN ('plan','offline')),
    user_id      UUID        REFERENCES users(id) ON DELETE SET NULL,
    server_id    UUID        REFERENCES servers(id) ON DELETE SET NULL,
    tier         TEXT        NOT NULL CHECK (tier IN ('home','pro')),
    seats        INTEGER     NOT NULL DEFAULT 0 CHECK (seats >= 0),
    holder       TEXT        NOT NULL DEFAULT '',
    issued_at    TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS licenses_issued_user_idx ON licenses_issued (user_id);

-- license_revocations: a license id here is on the signed list and a
-- home holding it locks at its next daily check. Removing the row
-- (unrevoke) lets it work again.
CREATE TABLE IF NOT EXISTS license_revocations (
    license_id  TEXT        PRIMARY KEY,
    reason      TEXT        NOT NULL,
    revoked_by  TEXT        NOT NULL,
    revoked_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS license_revocations;
DROP TABLE IF EXISTS licenses_issued;
