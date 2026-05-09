-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0006.
--
-- Substitutions match slot 0001's parity sibling: UUID → TEXT,
-- JSONB → TEXT (JSON), TIMESTAMPTZ → TEXT (ISO-8601), BIGSERIAL →
-- INTEGER PRIMARY KEY AUTOINCREMENT.
--
CREATE TABLE IF NOT EXISTS library_scan_state (
    library_id        TEXT     PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    last_scan_at      TEXT,
    last_scan_id      TEXT,
    in_progress       INTEGER  NOT NULL DEFAULT 0,
    files_seen        INTEGER  NOT NULL DEFAULT 0,
    files_inserted    INTEGER  NOT NULL DEFAULT 0,
    files_updated     INTEGER  NOT NULL DEFAULT 0,
    files_missing     INTEGER  NOT NULL DEFAULT 0,
    metadata          TEXT     NOT NULL DEFAULT '{}',
    updated_at        TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS purge_log (
    id             INTEGER  PRIMARY KEY AUTOINCREMENT,
    library_id     TEXT     NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    video_id       TEXT     NOT NULL,
    content_hash   TEXT     NOT NULL,
    path           TEXT     NOT NULL,
    missing_since  TEXT     NOT NULL,
    purged_at      TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS purge_log;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS library_scan_state;
-- +goose StatementEnd
