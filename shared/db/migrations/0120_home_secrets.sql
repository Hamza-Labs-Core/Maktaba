-- +goose Up
-- +goose StatementBegin
--
-- Slot 0120 (Epic 32, story 32.6) - home_secrets: the home's sealed secrets
-- (specs/epics/32-tv-streaming-cluster-remote/plan-32-06-cloudflare-integration.md section 2).
-- (Epic 32 reserved 0119 for this table; 0119 went to cluster_cache first, so it is the next
-- free slot.)
--
-- Every value is sealed by the home's API (api/internal/secrets) with AES-256-GCM under a
-- 32-byte key file in the home's config directory (secrets.key, mode 0600). The key never
-- enters the database, so a dump alone opens nothing. key_id is sha256(key)[:8] in hex: a
-- replaced key file shows as "this secret was sealed with another key" (re-enter it).
--
-- Holds the Cloudflare API token, the Stream signing key, later the live inputs' stream keys,
-- and every extension's secret setting (the Plex token, moved out of extensions.settings by
-- the API's first start after this slot, README Q7). The machines' group role never reads it.
--
CREATE TABLE IF NOT EXISTS home_secrets (
    name        TEXT        PRIMARY KEY,
    ciphertext  BYTEA       NOT NULL,
    nonce       BYTEA       NOT NULL,
    key_id      TEXT        NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by  UUID
);
-- +goose StatementEnd

-- +goose StatementBegin
-- The machines' group (slot 0103) gets data rights on every new table through its default
-- privileges: none on this one. Guarded like slot 0103: the role exists only when the
-- migrating user could create it.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        REVOKE ALL ON home_secrets FROM maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
REVOKE ALL ON home_secrets FROM PUBLIC;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS home_secrets;
-- +goose StatementEnd
