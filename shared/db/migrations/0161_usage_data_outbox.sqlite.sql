-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0161 (Epic 16, stories 16.5 and 16.7 reworked): the old
-- telemetry sink goes; the outbox of what waits to go to Maktaba Cloud comes. BIGSERIAL
-- becomes INTEGER PRIMARY KEY, TIMESTAMPTZ TEXT, JSONB TEXT.
--
DROP TABLE IF EXISTS telemetry_web_vitals;
DROP TABLE IF EXISTS telemetry_events;

CREATE TABLE IF NOT EXISTS telemetry_outbox (
    id              INTEGER PRIMARY KEY,
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    kind            TEXT    NOT NULL CHECK (kind IN ('server', 'app', 'forget')),
    install_id      TEXT    NOT NULL,
    body            TEXT    NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_error      TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS telemetry_outbox_next_idx
    ON telemetry_outbox (next_attempt_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS telemetry_outbox;
-- +goose StatementEnd
