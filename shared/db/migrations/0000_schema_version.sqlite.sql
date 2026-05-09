-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0000.
--
-- SQLite has no TIMESTAMPTZ; we use TEXT in ISO-8601 form so the row
-- compares cleanly across dialects. application code is responsible
-- for converting the value back to a time.Time.
--
CREATE TABLE IF NOT EXISTS maktaba_schema_version (
    id              INTEGER PRIMARY KEY,
    bootstrapped_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    notes           TEXT NOT NULL DEFAULT ''
);

INSERT OR IGNORE INTO maktaba_schema_version (id, notes)
VALUES (1, 'goose runner bootstrapped (Story 22.4)');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS maktaba_schema_version;
-- +goose StatementEnd
