-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0056 (plan-16-04) — licenses table. Single active license is the
-- norm; CHECK + partial unique index enforces that invariant.
--
CREATE TABLE IF NOT EXISTS licenses (
    license_id   TEXT         PRIMARY KEY,
    tier         TEXT         NOT NULL CHECK (tier IN ('free', 'premium')),
    seats        INTEGER      NOT NULL CHECK (seats >= 0),
    issued_at    TIMESTAMPTZ  NOT NULL,
    expires_at   TIMESTAMPTZ  NOT NULL,
    revoked_at   TIMESTAMPTZ,
    raw_jwt      TEXT         NOT NULL,
    features     JSONB        NOT NULL DEFAULT '[]'::jsonb
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS licenses_only_one_active
    ON licenses ((1)) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS licenses_only_one_active;
DROP TABLE IF EXISTS licenses;
-- +goose StatementEnd
