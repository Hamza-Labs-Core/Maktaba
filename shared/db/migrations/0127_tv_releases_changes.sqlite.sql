-- +goose Up
-- +goose StatementBegin
--
-- Slot 0127 (Epic 33, stories 33.2 and 33.10), SQLite twin: the same tables with TEXT
-- uuids, timestamps and JSON. The schedule generation sequence is a table whose rowid
-- allocates the next generation (SQLite has no sequences).
--
CREATE TABLE IF NOT EXISTS tv_releases (
    id                TEXT    PRIMARY KEY,
    digest            TEXT    NOT NULL UNIQUE,
    kinds             TEXT    NOT NULL DEFAULT '[]',
    sched_gen         INTEGER NOT NULL UNIQUE,
    streaming_version TEXT    NOT NULL DEFAULT '',
    streaming_sha256  TEXT    NOT NULL DEFAULT '',
    settings          TEXT    NOT NULL DEFAULT '{}',
    lineup_digest     TEXT    NOT NULL DEFAULT '',
    base_release_id   TEXT    REFERENCES tv_releases(id) ON DELETE SET NULL,
    created_at        TEXT    NOT NULL DEFAULT (datetime('now')),
    created_by        TEXT
);

CREATE TABLE IF NOT EXISTS tv_release_channels (
    release_id    TEXT    NOT NULL REFERENCES tv_releases(id) ON DELETE CASCADE,
    channel_id    TEXT    NOT NULL,
    def           TEXT    NOT NULL,
    art           TEXT    NOT NULL DEFAULT '{}',
    sched_changed INTEGER NOT NULL DEFAULT 0,
    boundary_at   TEXT,
    PRIMARY KEY (release_id, channel_id)
);

CREATE TABLE IF NOT EXISTS tv_changes (
    id              TEXT    PRIMARY KEY,
    number          INTEGER UNIQUE,
    release_id      TEXT    REFERENCES tv_releases(id) ON DELETE SET NULL,
    base_release_id TEXT    REFERENCES tv_releases(id) ON DELETE SET NULL,
    state           TEXT    NOT NULL DEFAULT 'draft' CHECK (state IN (
                        'draft', 'staged', 'verifying', 'ready', 'approved', 'swapping',
                        'live', 'superseded', 'failed', 'cancelled', 'rolled_back')),
    kinds           TEXT    NOT NULL DEFAULT '[]',
    title           TEXT    NOT NULL DEFAULT '',
    source          TEXT    NOT NULL DEFAULT '{}',
    change_set      TEXT    NOT NULL DEFAULT '{}',
    digest          TEXT,
    diff            TEXT    NOT NULL DEFAULT '{}',
    verify_report   TEXT    NOT NULL DEFAULT '{}',
    auto_rule       TEXT,
    approved_by     TEXT    NOT NULL DEFAULT '[]',
    approved_at     TEXT,
    anchor_at       TEXT,
    swap_at         TEXT,
    swap_wait       TEXT    CHECK (swap_wait IN ('boundary', 'immediate')),
    created_by      TEXT,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    staged_at       TEXT,
    ready_at        TEXT,
    swapped_at      TEXT,
    ended_at        TEXT,
    end_reason      TEXT,
    superseded_by   TEXT    REFERENCES tv_changes(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS tv_change_events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    change_id     TEXT    NOT NULL REFERENCES tv_changes(id) ON DELETE CASCADE,
    at            TEXT    NOT NULL DEFAULT (datetime('now')),
    actor_user_id TEXT,
    actor_source  TEXT    NOT NULL DEFAULT 'system',
    from_state    TEXT,
    to_state      TEXT,
    action        TEXT    NOT NULL,
    detail        TEXT    NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS tv_routing (
    scope               TEXT    PRIMARY KEY,
    live_release_id     TEXT    REFERENCES tv_releases(id) ON DELETE SET NULL,
    live_gen            INTEGER NOT NULL DEFAULT 0,
    previous_release_id TEXT    REFERENCES tv_releases(id) ON DELETE SET NULL,
    previous_gen        INTEGER,
    epoch               INTEGER NOT NULL DEFAULT 0,
    switched_at         TEXT,
    previous_until      TEXT
);

CREATE TABLE IF NOT EXISTS tv_sched_gen_seq (
    gen INTEGER PRIMARY KEY AUTOINCREMENT,
    at  TEXT    NOT NULL DEFAULT (datetime('now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS tv_changes_state_idx ON tv_changes (state, created_at);
CREATE INDEX IF NOT EXISTS tv_change_events_change_idx ON tv_change_events (change_id, id);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement)
VALUES
    ('tv_stage', 'Stage TV change', '["catalog"]', 'cpu', 'channels', 3, 1, 0, 0, 2, '[]', '[]', '[]', 0, 0, 0, 'auto', 'home'),
    ('tv_swap', 'Swap TV change', '["catalog"]', 'cpu', 'channels', 4, 1, 0, 0, 1, '[]', '[]', '[]', 0, 0, 0, 'auto', 'home'),
    ('tv_gc', 'TV staging clean-up', '["catalog"]', 'cpu', 'channels', 5, 1, 0, 0, 3, '[]', '[]', '[]', 0, 0, 0, 'auto', 'home')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage IN ('tv_stage', 'tv_swap', 'tv_gc');
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS tv_sched_gen_seq;
DROP TABLE IF EXISTS tv_routing;
DROP TABLE IF EXISTS tv_change_events;
DROP TABLE IF EXISTS tv_changes;
DROP TABLE IF EXISTS tv_release_channels;
DROP TABLE IF EXISTS tv_releases;
-- +goose StatementEnd
