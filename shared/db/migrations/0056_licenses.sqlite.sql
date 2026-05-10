-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS licenses (
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

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS licenses;
-- +goose StatementEnd
