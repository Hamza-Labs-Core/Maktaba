-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0063 (Epic 16 Story 16.2, HLB-289).
--
-- SQLite cannot ALTER a CHECK constraint in place, so the licenses
-- table is rebuilt with the widened free/home/pro domain (the
-- documented SQLite "create new / copy / drop / rename" recipe). The
-- column set mirrors the slot-0056 SQLite sibling exactly; only the
-- tier CHECK changes. Any stray legacy 'premium' row is mapped to
-- 'pro' during the copy (premium's feature set is pro's).
--
-- Idempotent: the rebuilt table is created under a temp name and the
-- swap uses DROP/ALTER ... RENAME, so a partial/repeat application
-- converges. This file uses goose's StatementBegin / StatementEnd
-- markers around each DDL statement.
--
CREATE TABLE IF NOT EXISTS licenses_0063_new (
    license_id   TEXT    PRIMARY KEY,
    tier         TEXT    NOT NULL CHECK (tier IN ('free', 'home', 'pro')),
    seats        INTEGER NOT NULL CHECK (seats >= 0),
    issued_at    TEXT    NOT NULL,
    expires_at   TEXT    NOT NULL,
    revoked_at   TEXT,
    raw_jwt      TEXT    NOT NULL,
    features     TEXT    NOT NULL DEFAULT '[]'
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT OR IGNORE INTO licenses_0063_new
    (license_id, tier, seats, issued_at, expires_at, revoked_at, raw_jwt, features)
SELECT
    license_id,
    CASE WHEN tier = 'premium' THEN 'pro' ELSE tier END,
    seats, issued_at, expires_at, revoked_at, raw_jwt, features
FROM licenses;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS licenses;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE licenses_0063_new RENAME TO licenses;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
--
-- Restore the slot-0056 free/premium domain (dev-only; lossy — home
-- and pro both collapse to premium).
--
CREATE TABLE IF NOT EXISTS licenses_0063_old (
    license_id   TEXT    PRIMARY KEY,
    tier         TEXT    NOT NULL CHECK (tier IN ('free', 'premium')),
    seats        INTEGER NOT NULL CHECK (seats >= 0),
    issued_at    TEXT    NOT NULL,
    expires_at   TEXT    NOT NULL,
    revoked_at   TEXT,
    raw_jwt      TEXT    NOT NULL,
    features     TEXT    NOT NULL DEFAULT '[]'
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT OR IGNORE INTO licenses_0063_old
    (license_id, tier, seats, issued_at, expires_at, revoked_at, raw_jwt, features)
SELECT
    license_id,
    CASE WHEN tier IN ('home', 'pro') THEN 'premium' ELSE tier END,
    seats, issued_at, expires_at, revoked_at, raw_jwt, features
FROM licenses;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS licenses;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE licenses_0063_old RENAME TO licenses;
-- +goose StatementEnd
