-- +goose Up
-- Staff accounts for the operator console (Epic 50 story 50.33 §1): apart from customer
-- accounts (no homes, plan, OAuth identity or email reset), created from the CLI only.
--
-- staff_accounts:        roles is a list ('service_admin' today); an account with none can
--                        sign in and see its own sessions, nothing else. The lockout state
--                        (§6) lives on the row: the failures in the window, and the lock.
-- staff_enrolments:      the one-time enrolment links (24 h), kept as SHA-256.
-- staff_totp:            the TOTP secret sealed with AES-256-GCM (MAKTABA_CLOUD_STAFF_SECRET_KEY),
--                        and the last step used, so a code is good once.
-- staff_passkeys:        WebAuthn credentials (part 2 of the story); a counter that goes
--                        backwards disables one.
-- staff_recovery_codes:  argon2id, each used once.
-- staff_mfa_tokens:      the step between password and second factor: 5 minutes, bound to the
--                        account and the client IP, 5 attempts.
-- staff_sessions:        the console's sessions: the refresh cookie as SHA-256 (and the one it
--                        replaced, to recognise a replay), the MFA sign-in and last activity
--                        (§4's idle timeout and cap), and the last MFA check (§5).
CREATE TABLE IF NOT EXISTS staff_accounts (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT        NOT NULL UNIQUE,
    password_hash   TEXT,
    roles           TEXT[]      NOT NULL DEFAULT '{}',
    status          TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    failed_at       TIMESTAMPTZ[] NOT NULL DEFAULT '{}',
    locked_until    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    enrolled_at     TIMESTAMPTZ,
    last_signin_at  TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS staff_enrolments (
    token_hash  TEXT        PRIMARY KEY,
    staff_id    UUID        NOT NULL REFERENCES staff_accounts(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS staff_totp (
    staff_id       UUID        PRIMARY KEY REFERENCES staff_accounts(id) ON DELETE CASCADE,
    secret_sealed  TEXT        NOT NULL,
    last_step      BIGINT      NOT NULL DEFAULT 0,
    confirmed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS staff_passkeys (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    staff_id       UUID        NOT NULL REFERENCES staff_accounts(id) ON DELETE CASCADE,
    credential_id  BYTEA       NOT NULL UNIQUE,
    public_key     BYTEA       NOT NULL,
    sign_count     BIGINT      NOT NULL DEFAULT 0,
    name           TEXT        NOT NULL DEFAULT '',
    disabled_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at   TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS staff_recovery_codes (
    id         BIGSERIAL   PRIMARY KEY,
    staff_id   UUID        NOT NULL REFERENCES staff_accounts(id) ON DELETE CASCADE,
    code_hash  TEXT        NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_staff_recovery_codes_staff ON staff_recovery_codes (staff_id);

CREATE TABLE IF NOT EXISTS staff_mfa_tokens (
    token_hash  TEXT        PRIMARY KEY,
    staff_id    UUID        NOT NULL REFERENCES staff_accounts(id) ON DELETE CASCADE,
    ip          TEXT        NOT NULL,
    attempts    INT         NOT NULL DEFAULT 0,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS staff_sessions (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    staff_id         UUID        NOT NULL REFERENCES staff_accounts(id) ON DELETE CASCADE,
    token_hash       TEXT        NOT NULL UNIQUE,
    prev_token_hash  TEXT,
    rotated_at       TIMESTAMPTZ,
    amr              TEXT[]      NOT NULL,
    signed_in_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_active_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    auth_time        TIMESTAMPTZ NOT NULL DEFAULT now(),
    ip               TEXT        NOT NULL DEFAULT '',
    user_agent       TEXT        NOT NULL DEFAULT '',
    revoked_at       TIMESTAMPTZ,
    revoke_reason    TEXT
);
CREATE INDEX IF NOT EXISTS idx_staff_sessions_staff ON staff_sessions (staff_id);
CREATE INDEX IF NOT EXISTS idx_staff_sessions_prev ON staff_sessions (prev_token_hash);

-- +goose Down
DROP TABLE IF EXISTS staff_sessions;
DROP TABLE IF EXISTS staff_mfa_tokens;
DROP TABLE IF EXISTS staff_recovery_codes;
DROP TABLE IF EXISTS staff_passkeys;
DROP TABLE IF EXISTS staff_totp;
DROP TABLE IF EXISTS staff_enrolments;
DROP TABLE IF EXISTS staff_accounts;
