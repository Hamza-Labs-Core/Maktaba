-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0155 (Epic 16 story 16.4): the license server's last
-- answer, kept across a restart. TIMESTAMPTZ becomes TEXT. A SQLite home is a single
-- machine: no copy exclusion to extend.
--
CREATE TABLE IF NOT EXISTS license_state (
    id                  INTEGER PRIMARY KEY CHECK (id = 1),
    revocations         TEXT    NOT NULL DEFAULT '',
    revocations_at      TEXT,
    clock_high_water    TEXT,
    last_refresh_at     TEXT,
    last_refresh_error  TEXT    NOT NULL DEFAULT '',
    lock_reason         TEXT    NOT NULL DEFAULT ''
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS license_state;
-- +goose StatementEnd
