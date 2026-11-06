-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0191 (plugins: the plugin API's tables). Arrays are JSON
-- text and there is no NOTIFY. SQLite cannot widen job_stages' inline length check or
-- chapters' source check without rebuilding the tables: plugins run on a Postgres home
-- (the API serves the plugin socket only there), so this sibling keeps the schema in
-- step and leaves those two checks as they are.
--
CREATE TABLE IF NOT EXISTS plugin_installs (
    plugin_id         TEXT PRIMARY KEY,
    version           TEXT NOT NULL,
    digest            TEXT NOT NULL,
    path              TEXT NOT NULL,
    manifest          TEXT NOT NULL,
    review_level      TEXT NOT NULL DEFAULT 'none'
                      CHECK (review_level IN ('none', 'developer', 'store', 'development')),
    installed_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    previous_version  TEXT,
    previous_digest   TEXT,
    previous_path     TEXT,
    previous_manifest TEXT
);

CREATE TABLE IF NOT EXISTS plugin_grants (
    plugin_id     TEXT PRIMARY KEY,
    capabilities  TEXT    NOT NULL DEFAULT '[]',
    libraries     TEXT    NOT NULL DEFAULT '[]',
    all_libraries INTEGER NOT NULL DEFAULT 0,
    generation    INTEGER NOT NULL DEFAULT 1,
    granted_by    TEXT    NOT NULL DEFAULT '',
    granted_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS plugin_invocations (
    id               TEXT PRIMARY KEY,
    token_hash       TEXT    NOT NULL UNIQUE,
    plugin_id        TEXT    NOT NULL,
    version          TEXT    NOT NULL,
    job_id           INTEGER NOT NULL,
    stage            TEXT    NOT NULL,
    subject_kind     TEXT    NOT NULL,
    subject_id       TEXT    NOT NULL,
    libraries        TEXT    NOT NULL DEFAULT '[]',
    all_libraries    INTEGER NOT NULL DEFAULT 0,
    grants           TEXT    NOT NULL DEFAULT '[]',
    grant_generation INTEGER NOT NULL,
    exp              TEXT    NOT NULL,
    calls            INTEGER NOT NULL DEFAULT 0,
    bytes_written    INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ended_at         TEXT
);
CREATE INDEX IF NOT EXISTS plugin_invocations_created_idx ON plugin_invocations (created_at);

CREATE TABLE IF NOT EXISTS plugin_data (
    plugin_id    TEXT    NOT NULL,
    subject_kind TEXT    NOT NULL,
    subject_id   TEXT    NOT NULL,
    key          TEXT    NOT NULL,
    value        TEXT    NOT NULL,
    exported     INTEGER NOT NULL DEFAULT 0,
    bytes        INTEGER NOT NULL,
    updated_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (plugin_id, subject_kind, subject_id, key)
);

CREATE TABLE IF NOT EXISTS plugin_writes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    plugin_id   TEXT NOT NULL,
    version     TEXT NOT NULL,
    job_id      INTEGER,
    target      TEXT NOT NULL,
    subject_id  TEXT NOT NULL,
    field       TEXT NOT NULL DEFAULT '',
    before      TEXT,
    after       TEXT,
    at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    reverted_at TEXT
);
CREATE INDEX IF NOT EXISTS plugin_writes_plugin_idx ON plugin_writes (plugin_id, at);

CREATE TABLE IF NOT EXISTS plugin_suggestions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    plugin_id    TEXT NOT NULL,
    version      TEXT NOT NULL,
    job_id       INTEGER,
    subject_kind TEXT NOT NULL,
    subject_id   TEXT NOT NULL,
    field        TEXT NOT NULL,
    value        TEXT NOT NULL,
    reason       TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'accepted', 'declined')),
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    decided_at   TEXT,
    decided_by   TEXT
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS plugin_suggestions;
DROP TABLE IF EXISTS plugin_writes;
DROP TABLE IF EXISTS plugin_data;
DROP TABLE IF EXISTS plugin_invocations;
DROP TABLE IF EXISTS plugin_grants;
DROP TABLE IF EXISTS plugin_installs;
-- +goose StatementEnd
