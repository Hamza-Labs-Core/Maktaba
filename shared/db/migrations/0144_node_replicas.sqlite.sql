-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0144 (node replicas). A SQLite home has no machines
-- and no roles; the columns exist so the same code runs on both (the reporting
-- function is Postgres only: no machine reaches a SQLite home's database).
--
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS replica TEXT NOT NULL DEFAULT '{}';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS replica_reported_at TEXT;
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
CREATE TABLE IF NOT EXISTS cluster_switchovers (
    id            TEXT PRIMARY KEY,
    term          INTEGER NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('planned', 'emergency')),
    to_node       TEXT NOT NULL,
    to_name       TEXT NOT NULL,
    from_name     TEXT NOT NULL,
    state         TEXT NOT NULL CHECK (state IN ('checking', 'freezing', 'frozen', 'handing_over',
                      'promoting', 'repointing', 'done', 'failed', 'cancelled')),
    detail        TEXT,
    integrations  TEXT NOT NULL DEFAULT '{}',
    machines      TEXT NOT NULL DEFAULT '{}',
    requested_by  TEXT,
    requested_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    frozen_at     TEXT,
    done_at       TEXT,
    updated_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_switchovers;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_terms;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_replica_beat;
-- +goose StatementEnd
