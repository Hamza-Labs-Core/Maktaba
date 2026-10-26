-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0156 (Epic 16, story 16.7): the opt-in telemetry sink.
-- BIGSERIAL becomes INTEGER PRIMARY KEY, TIMESTAMPTZ TEXT, JSONB TEXT, DOUBLE PRECISION
-- REAL.
--
CREATE TABLE IF NOT EXISTS telemetry_events (
    id               INTEGER PRIMARY KEY,
    received_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    occurred_at      TEXT,
    device_pseudonym TEXT    NOT NULL,
    event_kind       TEXT    NOT NULL,
    app_version      TEXT,
    os               TEXT,
    os_version       TEXT,
    locale           TEXT,
    payload          TEXT    NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS telemetry_events_received_at_idx
    ON telemetry_events (received_at);
CREATE INDEX IF NOT EXISTS telemetry_events_kind_received_idx
    ON telemetry_events (event_kind, received_at);
CREATE INDEX IF NOT EXISTS telemetry_events_pseudonym_idx
    ON telemetry_events (device_pseudonym);

CREATE TABLE IF NOT EXISTS telemetry_web_vitals (
    id               INTEGER PRIMARY KEY,
    received_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    device_pseudonym TEXT    NOT NULL,
    metric           TEXT    NOT NULL CHECK (metric IN ('LCP', 'FID', 'CLS', 'INP', 'TTFB')),
    value            REAL    NOT NULL,
    route            TEXT
);
CREATE INDEX IF NOT EXISTS telemetry_web_vitals_received_at_idx
    ON telemetry_web_vitals (received_at);
CREATE INDEX IF NOT EXISTS telemetry_web_vitals_pseudonym_idx
    ON telemetry_web_vitals (device_pseudonym);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS telemetry_web_vitals;
DROP TABLE IF EXISTS telemetry_events;
-- +goose StatementEnd
