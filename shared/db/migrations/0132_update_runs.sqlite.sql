-- +goose Up
-- +goose StatementBegin
--
-- Slot 0132 (Epic 35, slot S1), SQLite twin: automatic updates with TEXT ids,
-- timestamps and JSON. SQLite's audit_log has no category check (slot 0054) and SQLite
-- has no roles, so there is nothing to widen or revoke.
--
CREATE TABLE IF NOT EXISTS release_catalog (
    version          TEXT    PRIMARY KEY,
    tag              TEXT    NOT NULL,
    channel          TEXT    NOT NULL DEFAULT 'stable' CHECK (channel IN ('stable', 'beta')),
    published_at     TEXT,
    prerelease       INTEGER NOT NULL DEFAULT 0,
    release_url      TEXT    NOT NULL DEFAULT '',
    notes            TEXT    NOT NULL DEFAULT '',
    assets           TEXT    NOT NULL DEFAULT '[]',
    manifest         TEXT,
    checksums_sha256 TEXT    NOT NULL DEFAULT '',
    verify_state     TEXT    NOT NULL DEFAULT 'unverified'
                             CHECK (verify_state IN ('unverified', 'verified', 'failed')),
    verify_error     TEXT,
    first_seen_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    verified_at      TEXT,
    withdrawn_at     TEXT,
    blocked_reason   TEXT    NOT NULL DEFAULT '',
    held_at          TEXT,
    approved_at      TEXT,
    approved_by      TEXT,
    attempts         INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS update_runs (
    id             TEXT PRIMARY KEY,
    version        TEXT NOT NULL,
    from_version   TEXT NOT NULL DEFAULT '',
    trigger        TEXT NOT NULL CHECK (trigger IN ('auto', 'manual', 'catch_up', 'retry')),
    state          TEXT NOT NULL DEFAULT 'planned',
    step           TEXT NOT NULL DEFAULT '',
    started_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at    TEXT,
    error          TEXT,
    backup         TEXT,
    actor_user_id  TEXT,
    journal_digest TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS update_runs_one_active_idx ON update_runs ((1))
    WHERE finished_at IS NULL;
CREATE INDEX IF NOT EXISTS update_runs_started_idx ON update_runs (started_at DESC);

CREATE TABLE IF NOT EXISTS update_run_steps (
    run_id      TEXT    NOT NULL REFERENCES update_runs(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    host_id     TEXT    NOT NULL DEFAULT 'home',
    step        TEXT    NOT NULL,
    state       TEXT    NOT NULL,
    started_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at TEXT,
    detail      TEXT    NOT NULL DEFAULT '{}',
    PRIMARY KEY (run_id, seq)
);

CREATE TABLE IF NOT EXISTS update_targets (
    run_id       TEXT    NOT NULL REFERENCES update_runs(id) ON DELETE CASCADE,
    node_id      TEXT    NOT NULL,
    ord          INTEGER NOT NULL DEFAULT 0,
    platform     TEXT    NOT NULL DEFAULT '',
    state        TEXT    NOT NULL DEFAULT 'pending',
    from_version TEXT    NOT NULL DEFAULT '',
    to_version   TEXT    NOT NULL DEFAULT '',
    attempts     INTEGER NOT NULL DEFAULT 0,
    error        TEXT,
    prepared_at  TEXT,
    applied_at   TEXT,
    healthy_at   TEXT,
    PRIMARY KEY (run_id, node_id)
);

CREATE TABLE IF NOT EXISTS update_watermark (
    id                INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    highest_installed TEXT    NOT NULL,
    set_at            TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS update_watermark;
DROP TABLE IF EXISTS update_targets;
DROP TABLE IF EXISTS update_run_steps;
DROP TABLE IF EXISTS update_runs;
DROP TABLE IF EXISTS release_catalog;
-- +goose StatementEnd
