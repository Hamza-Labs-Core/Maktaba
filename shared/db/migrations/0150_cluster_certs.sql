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
-- Additive only; the certificates stay the home API's (nothing for maktaba_worker).
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
CREATE INDEX IF NOT EXISTS cluster_certs_node_idx ON cluster_certs (node_id);
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS cert_serial TEXT;
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS cert_not_after TIMESTAMPTZ;
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS link_since TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS link_since;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS cert_not_after;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS cert_serial;
DROP TABLE IF EXISTS cluster_certs;
-- +goose StatementEnd
