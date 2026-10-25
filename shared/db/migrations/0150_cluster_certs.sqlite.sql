-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0150: the cluster link's certificates (TEXT ids and
-- timestamps) and the machines' current certificate serial, expiry and link_since.
--
CREATE TABLE IF NOT EXISTS cluster_certs (
    serial      TEXT PRIMARY KEY,
    node_id     TEXT NULL,
    kind        TEXT NOT NULL,
    subject     TEXT NOT NULL DEFAULT '',
    sha256      TEXT NOT NULL DEFAULT '',
    not_before  TEXT NOT NULL,
    not_after   TEXT NOT NULL,
    issued_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at  TEXT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS cluster_certs_node_idx ON cluster_certs (node_id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS cert_serial TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS cert_not_after TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS link_since TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
