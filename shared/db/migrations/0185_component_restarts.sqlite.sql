-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0185 (a restart for components whose loops run in another
-- process): component_restarts, and the triggers that move component_meta.revision on
-- each write (SQLite has no NOTIFY: the runners poll the revision).
--
CREATE TABLE IF NOT EXISTS component_restarts (
    machine      TEXT    NOT NULL,
    id           TEXT    NOT NULL,
    host         TEXT    NOT NULL CHECK (host IN ('api', 'streaming', 'pipeline', 'supervisor')),
    seq          INTEGER NOT NULL DEFAULT 1,
    requested_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    requested_by TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (machine, id, host)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS component_restarts_revision_ins
    AFTER INSERT ON component_restarts
    FOR EACH ROW
BEGIN
    UPDATE component_meta SET revision = revision + 1 WHERE singleton = 1;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS component_restarts_revision_upd
    AFTER UPDATE ON component_restarts
    FOR EACH ROW
BEGIN
    UPDATE component_meta SET revision = revision + 1 WHERE singleton = 1;
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS component_restarts_revision_upd;
DROP TRIGGER IF EXISTS component_restarts_revision_ins;
DROP TABLE IF EXISTS component_restarts;
-- +goose StatementEnd
