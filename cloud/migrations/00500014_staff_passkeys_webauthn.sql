-- +goose Up
-- Story 50.33 part 2: passkeys (WebAuthn) for the console's staff accounts.
--
-- staff_passkeys.credential:  the whole WebAuthn credential record as go-webauthn keeps it
--                             (transports, flags, AAGUID, attestation format), beside the
--                             columns the console lists; the counter check reads sign_count.
-- staff_webauthn_challenges:  a ceremony under way, one per account and purpose (adding a
--                             passkey, signing in, the re-check): its challenge and options,
--                             5 minutes, deleted as it is used, so an answer is good once.
ALTER TABLE staff_passkeys ADD COLUMN IF NOT EXISTS credential JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE IF NOT EXISTS staff_webauthn_challenges (
    staff_id    UUID        NOT NULL REFERENCES staff_accounts(id) ON DELETE CASCADE,
    purpose     TEXT        NOT NULL CHECK (purpose IN ('register','login','reauth')),
    session     JSONB       NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (staff_id, purpose)
);

-- +goose Down
DROP TABLE IF EXISTS staff_webauthn_challenges;
ALTER TABLE staff_passkeys DROP COLUMN IF EXISTS credential;
