-- +goose Up
-- Identity tables for Epic 25 stories 25.2–25.5.
--
-- users:        primary identity record. `password_hash` may be NULL
--               for OAuth-only signups (Google/Apple).
-- oauth_links:  external identity provider associations. A user can
--               have at most one link per (provider, subject) — we
--               unique-index it so re-linking is idempotent.
-- sessions:     opaque session records (refresh tokens hashed at rest).
-- email_verifications:  pending verification tokens (single-use).
CREATE TABLE IF NOT EXISTS users (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT         NOT NULL UNIQUE,
    email_verified  BOOLEAN      NOT NULL DEFAULT FALSE,
    password_hash   TEXT,
    display_name    TEXT,
    locale          TEXT         NOT NULL DEFAULT 'en',
    avatar_url      TEXT,
    plan            TEXT         NOT NULL DEFAULT 'free'
                     CHECK (plan IN ('free','pro','family')),
    status          TEXT         NOT NULL DEFAULT 'active',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_login_at   TIMESTAMPTZ
);
CREATE INDEX idx_users_email_lower ON users (lower(email));

CREATE TABLE IF NOT EXISTS oauth_links (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider    TEXT         NOT NULL,
    subject     TEXT         NOT NULL,
    email       TEXT,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (provider, subject)
);
CREATE INDEX idx_oauth_links_user ON oauth_links (user_id);

CREATE TABLE IF NOT EXISTS sessions (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash  TEXT         NOT NULL UNIQUE,
    user_agent          TEXT,
    ip                  INET,
    expires_at          TIMESTAMPTZ  NOT NULL,
    revoked_at          TIMESTAMPTZ,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_sessions_user ON sessions (user_id);
CREATE INDEX idx_sessions_expires ON sessions (expires_at);

CREATE TABLE IF NOT EXISTS email_verifications (
    token_hash  TEXT         PRIMARY KEY,
    user_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose     TEXT         NOT NULL,
    expires_at  TIMESTAMPTZ  NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS email_verifications;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS oauth_links;
DROP TABLE IF EXISTS users;
