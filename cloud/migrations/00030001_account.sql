-- +goose Up
-- Account management: email change flow, account deletion holds.
CREATE TABLE IF NOT EXISTS email_change_requests (
    token_hash  TEXT         PRIMARY KEY,
    user_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    new_email   TEXT         NOT NULL,
    expires_at  TIMESTAMPTZ  NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS account_deletions (
    user_id     UUID         PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    purge_after TIMESTAMPTZ  NOT NULL,
    cancelled_at TIMESTAMPTZ
);

-- +goose Down
DROP TABLE IF EXISTS account_deletions;
DROP TABLE IF EXISTS email_change_requests;
