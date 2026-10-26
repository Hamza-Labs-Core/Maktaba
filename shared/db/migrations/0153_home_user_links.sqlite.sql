-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0153. Linked accounts (Epic 42 story 42.7): a home user links one Maktaba
-- Cloud account, and that account can then sign in to this home through
-- the cloud (a one-use assertion the cloud signs, verified here). The
-- home decides everything: the rights are the home user's, and the
-- link's scope caps them.
--
--   home_user_links      one cloud account per home user and one home user per cloud
--                        account; scope 'play' (default: watch only) or 'manage' (an
--                        admin's own link, set only on the home by that admin)
--   cloud_link_nonces    the link step's nonce: binds a cloud link assertion to the
--                        home user who started it (hashed, a few minutes)
--   cloud_assertions_used the assertions already used (one use each), kept until expiry
--   cloud_sign_ins       which web sessions and refresh families a cloud sign-in made,
--                        with the link's scope then; unlinking revokes them
CREATE TABLE IF NOT EXISTS home_user_links (
    user_id       TEXT        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    cloud_user_id TEXT        NOT NULL UNIQUE,
    cloud_label   TEXT,
    scope         TEXT        NOT NULL DEFAULT 'play' CHECK (scope IN ('play', 'manage')),
    created_at    TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cloud_link_nonces (
    nonce_hash TEXT        PRIMARY KEY,
    user_id    TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TEXT        NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cloud_assertions_used (
    jti        TEXT        PRIMARY KEY,
    expires_at TEXT        NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cloud_sign_ins (
    id         TEXT        PRIMARY KEY,
    kind       TEXT        NOT NULL CHECK (kind IN ('session', 'refresh')),
    user_id    TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope      TEXT        NOT NULL CHECK (scope IN ('play', 'manage')),
    created_at TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS cloud_sign_ins_user ON cloud_sign_ins (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS cloud_sign_ins;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS cloud_assertions_used;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS cloud_link_nonces;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS home_user_links;
-- +goose StatementEnd
