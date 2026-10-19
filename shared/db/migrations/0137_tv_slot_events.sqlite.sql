-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0137 (Epic 33, story 33.13 AC1): the TV slots' front
-- door events (failover, staging crash loop) the API turns into notices. A SQLite home
-- runs the engine in one process (no TV slots), so nothing writes the table: it exists
-- for the same shape. JSONB becomes TEXT, TIMESTAMPTZ TEXT.
--
CREATE TABLE IF NOT EXISTS tv_slot_events (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    host_id TEXT    NOT NULL,
    kind    TEXT    NOT NULL CHECK (kind IN ('failover', 'crash_loop')),
    slot    TEXT    NOT NULL DEFAULT '',
    detail  TEXT    NOT NULL DEFAULT '{}',
    at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tv_slot_events;
-- +goose StatementEnd
