-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0136 (Epic 33, stories 33.7 and 33.9): the swap and
-- rollback operations. A SQLite home runs the engine in one process (no TV slots), so
-- the swap commit makes the change live itself and no front door reads these tables;
-- they exist for the same shape. UUID becomes TEXT, JSONB TEXT, TIMESTAMPTZ TEXT,
-- BOOLEAN INTEGER.
--
CREATE TABLE IF NOT EXISTS tv_swap_ops (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    change_id       TEXT,
    kind            TEXT    NOT NULL CHECK (kind IN ('swap', 'rollback')),
    scope           TEXT    NOT NULL DEFAULT 'cluster',
    release_id      TEXT,
    from_release_id TEXT,
    epoch           INTEGER NOT NULL DEFAULT 0,
    reason          TEXT    NOT NULL DEFAULT '',
    state           TEXT    NOT NULL DEFAULT 'planned'
                            CHECK (state IN ('planned', 'switching', 'done', 'failed', 'cancelled')),
    committed       INTEGER NOT NULL DEFAULT 0,
    created_by      TEXT    NOT NULL DEFAULT '',
    host_id         TEXT,
    from_slot       TEXT,
    to_slot         TEXT,
    detail          TEXT    NOT NULL DEFAULT '{}',
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    started_at      TEXT,
    done_at         TEXT
);

CREATE TABLE IF NOT EXISTS tv_channel_switch (
    op_id       INTEGER NOT NULL REFERENCES tv_swap_ops(id) ON DELETE CASCADE,
    change_id   TEXT,
    channel_id  TEXT    NOT NULL,
    switch_at   TEXT    NOT NULL,
    state       TEXT    NOT NULL DEFAULT 'planned'
                        CHECK (state IN ('planned', 'switching', 'switched', 'skipped')),
    removed     INTEGER NOT NULL DEFAULT 0,
    switched_at TEXT,
    detail      TEXT    NOT NULL DEFAULT '{}',
    PRIMARY KEY (op_id, channel_id)
);

INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement)
VALUES
    ('tv_rollback', 'Roll back TV change', '["catalog"]', 'cpu', 'channels', 6, 1, 0, 0, 2, '[]', '[]', '[]', 0, 0, 0, 'auto', 'home')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'tv_rollback';
DROP TABLE IF EXISTS tv_channel_switch;
DROP TABLE IF EXISTS tv_swap_ops;
-- +goose StatementEnd
