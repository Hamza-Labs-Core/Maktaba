-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0150 (Epic 35 story 35.16, design §18): the cluster link's certificates. The home
-- runs a small CA (its key on the home only: cluster-ca.key beside secrets.key and a
-- sealed copy in home_secrets) and gives every machine a certificate; every certificate it
-- issues is a cluster_certs row (a machine's: node_id set; the home's own server,
-- pg_server, client and web certificates: node_id NULL). cluster_nodes gains the serial of
-- the machine's current certificate (a link connection must present it), its expiry (the
-- Machines page), and link_since: since when the machine has held a control session
-- without a break (the home switches the link to `require` once every machine has held
-- one for a day).
--
-- Additive only; the certificates stay the home API's: nothing for maktaba_worker, and no
-- machine's copy holds them (Epic 37: like cluster_nodes, the machines' own; the copy's
-- exclusion gains cluster_certs). NO TRANSACTION for the index (CONCURRENTLY): every
-- statement is safe to run again.
--
CREATE TABLE IF NOT EXISTS cluster_certs (
    serial      TEXT PRIMARY KEY,
    node_id     UUID NULL,
    kind        TEXT NOT NULL,
    subject     TEXT NOT NULL DEFAULT '',
    sha256      TEXT NOT NULL DEFAULT '',
    not_before  TIMESTAMPTZ NOT NULL,
    not_after   TIMESTAMPTZ NOT NULL,
    issued_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS cluster_certs_node_idx ON cluster_certs (node_id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS cert_serial TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS cert_not_after TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS link_since TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- slot 0103's default privileges gave the group role rights on new tables
        REVOKE ALL ON cluster_certs FROM maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0147's exclusion, with the certificates: the machines' own, like cluster_nodes
CREATE OR REPLACE FUNCTION cluster_copy_excluded() RETURNS text[]
LANGUAGE sql IMMUTABLE AS $f$
    SELECT ARRAY[
        -- secrets
        'home_secrets', 'tv_cluster_secret', 'tv_push_secrets', 'tv_simulcast_destinations',
        'refresh_tokens', 'password_reset_tokens', 'pairing_tickets', 'cluster_sso_codes',
        'idempotency_keys', 'licenses', 'devices', 'device_downloads', 'audit_log',
        -- live cookies (their hashes are copied: web_session_keys)
        'web_sessions',
        -- the machines' own (a machine's row of cluster_nodes is published to it alone)
        'cluster_nodes', 'cluster_node_stages', 'cluster_metrics_cache', 'cluster_log_cache',
        'cluster_cache_files', 'cluster_certs',
        -- what an old home wrote after an emergency took the home from it (the home's to
        -- apply or not)
        'cluster_kept_aside',
        -- each database's own
        'goose_db_version', 'maktaba_schema_version'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION cluster_copy_excluded() RETURNS text[]
LANGUAGE sql IMMUTABLE AS $f$
    SELECT ARRAY[
        'home_secrets', 'tv_cluster_secret', 'tv_push_secrets', 'tv_simulcast_destinations',
        'refresh_tokens', 'password_reset_tokens', 'pairing_tickets', 'cluster_sso_codes',
        'idempotency_keys', 'licenses', 'devices', 'device_downloads', 'audit_log',
        'web_sessions',
        'cluster_nodes', 'cluster_node_stages', 'cluster_metrics_cache', 'cluster_log_cache',
        'cluster_cache_files',
        'cluster_kept_aside',
        'goose_db_version', 'maktaba_schema_version'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS link_since;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS cert_not_after;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS cert_serial;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_certs;
-- +goose StatementEnd
