-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0103 (Epic 31). The same tables and columns; no
-- database roles (a SQLite home cannot take other machines: nothing but this process
-- opens its file), so the maktaba_worker group is Postgres only.
--
CREATE TABLE IF NOT EXISTS instance_identity (
    id                  INTEGER PRIMARY KEY CHECK (id = 1),
    instance_id         TEXT NOT NULL,
    name                TEXT NOT NULL,
    accept_joins        INTEGER NOT NULL DEFAULT 0,
    accept_joins_until  TEXT,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cluster_nodes (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    state            TEXT NOT NULL DEFAULT 'pending'
                     CHECK (state IN ('pending', 'approved', 'rejected', 'revoked')),
    os               TEXT,
    arch             TEXT,
    versions         TEXT NOT NULL DEFAULT '{}',
    capabilities     TEXT NOT NULL DEFAULT '{}',
    storage          TEXT NOT NULL DEFAULT '{}',
    path_map         TEXT,
    path_map_source  TEXT CHECK (path_map_source IN ('inferred', 'admin')),
    drain            INTEGER NOT NULL DEFAULT 0,
    node_pubkey      TEXT NOT NULL,
    home_pubkey      TEXT,
    home_privkey     TEXT,
    code_attempts    INTEGER NOT NULL DEFAULT 0,
    expires_at       TEXT,
    sealed           TEXT,
    db_role          TEXT,
    remote_addr      TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    approved_at      TEXT,
    approved_by      TEXT,
    revoked_at       TEXT,
    last_seen_at     TEXT,
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cluster_node_stages (
    node_id  TEXT NOT NULL REFERENCES cluster_nodes(id) ON DELETE CASCADE,
    stage    TEXT NOT NULL REFERENCES job_stages(stage) ON DELETE CASCADE,
    enabled  INTEGER NOT NULL,
    PRIMARY KEY (node_id, stage)
);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE workers ADD COLUMN IF NOT EXISTS node_id TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE workers ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'home';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE workers ADD COLUMN IF NOT EXISTS os TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE workers ADD COLUMN IF NOT EXISTS capabilities TEXT NOT NULL DEFAULT '{}';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE workers ADD COLUMN IF NOT EXISTS stages_off TEXT NOT NULL DEFAULT '{}';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE job_stages ADD COLUMN IF NOT EXISTS placement TEXT NOT NULL DEFAULT 'any';
-- +goose StatementEnd
-- +goose StatementBegin
UPDATE job_stages SET placement = 'home' WHERE stage IN ('index', 'thumb_small', 'review_pages');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_node_stages;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_nodes;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS instance_identity;
-- +goose StatementEnd
