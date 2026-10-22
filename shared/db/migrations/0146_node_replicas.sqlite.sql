-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0146 (node replicas). A SQLite home has no machines
-- and no roles; the columns exist so the same code runs on both (the reporting
-- function is Postgres only: no machine reaches a SQLite home's database).
--
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS replica TEXT NOT NULL DEFAULT '{}';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS replica_reported_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS standby INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS standby_kit TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cluster_kept_aside (
    id           TEXT PRIMARY KEY,
    term         INTEGER NOT NULL,
    from_node    TEXT NOT NULL,
    from_name    TEXT NOT NULL,
    cut_lsn      TEXT,
    report       TEXT NOT NULL,
    lost         INTEGER NOT NULL DEFAULT 0,
    state        TEXT NOT NULL DEFAULT 'reported' CHECK (state IN ('reported', 'applied', 'dismissed')),
    applied      TEXT,
    applied_at   TEXT,
    applied_by   TEXT,
    received_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (term, from_node)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cluster_replica_beat (
    id      INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    at      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ca_pem  TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT OR IGNORE INTO cluster_replica_beat (id) VALUES (1);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cluster_terms (
    term        INTEGER PRIMARY KEY CHECK (term >= 1),
    home_name   TEXT NOT NULL,
    home_node   TEXT,
    reason      TEXT NOT NULL CHECK (reason IN ('initial', 'planned', 'emergency')),
    started_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    cut_lsn     TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO cluster_terms (term, home_name, reason)
SELECT 1, COALESCE((SELECT name FROM instance_identity WHERE id = 1), 'home'), 'initial'
WHERE NOT EXISTS (SELECT 1 FROM cluster_terms);
-- +goose StatementEnd

-- +goose StatementBegin
-- The web sessions as a copy holds them (Postgres keeps them from web_sessions by
-- triggers; a SQLite home keeps no copies): parity only.
CREATE TABLE IF NOT EXISTS web_session_keys (
    id_sha256       TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL,
    csrf_sha256     TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    expires_at      TEXT NOT NULL,
    revoked_at      TEXT,
    origin_node_id  TEXT
);

CREATE TABLE IF NOT EXISTS cluster_switchovers (
    id            TEXT PRIMARY KEY,
    term          INTEGER NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('planned', 'emergency')),
    to_node       TEXT NOT NULL,
    to_name       TEXT NOT NULL,
    from_name     TEXT NOT NULL,
    state         TEXT NOT NULL CHECK (state IN ('checking', 'freezing', 'frozen', 'handing_over',
                      'promoting', 'waiting_new_home', 'repointing', 'done', 'failed', 'cancelled')),
    detail        TEXT,
    steps         TEXT NOT NULL DEFAULT '[]',
    integrations  TEXT NOT NULL DEFAULT '{}',
    machines      TEXT NOT NULL DEFAULT '{}',
    notices       TEXT NOT NULL DEFAULT '{}',
    requested_by  TEXT,
    requested_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    frozen_at     TEXT,
    done_at       TEXT,
    updated_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS cluster_switchovers_one_open ON cluster_switchovers ((1))
    WHERE state NOT IN ('done', 'failed', 'cancelled');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_kept_aside;
DROP TABLE IF EXISTS cluster_switchovers;
DROP TABLE IF EXISTS web_session_keys;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_terms;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_replica_beat;
-- +goose StatementEnd
