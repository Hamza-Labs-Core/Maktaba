-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0156 (Epic 16, story 16.7) - the opt-in telemetry sink. A device that opted in
-- (story 16.5) posts allow-listed events and Web Vitals under a pseudonym it made at
-- opt-in: 16 random characters, never linked to a user. No IP address is stored: the
-- API reads it for its per-IP rate limit only.
--
-- `occurred_at` is when the device says the event happened, never later than
-- `received_at` (a clock ahead of the server's is truncated to it). `payload` holds only
-- the fields the API allows for the kind, library roots and home folders replaced.
--
-- Retention (the API's nightly sweep): events 90 days, Web Vitals 30 days, both by
-- `received_at`. "Forget my device" deletes every row of a pseudonym.
--
-- Not in a machine's copy: slot 0155 adds both tables to cluster_copy_excluded().
-- maktaba_worker: the default rights (no narrowing, as 0152). Additive. NO TRANSACTION for
-- the indexes (CONCURRENTLY), as slot 0150.
--
CREATE TABLE IF NOT EXISTS telemetry_events (
    id               BIGSERIAL   PRIMARY KEY,
    received_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    occurred_at      TIMESTAMPTZ,
    device_pseudonym TEXT        NOT NULL,
    event_kind       TEXT        NOT NULL,
    app_version      TEXT,
    os               TEXT,
    os_version       TEXT,
    locale           TEXT,
    payload          JSONB       NOT NULL DEFAULT '{}'::jsonb
);

-- FID was replaced by INP in Web Vitals v3; it is still accepted from older clients.
CREATE TABLE IF NOT EXISTS telemetry_web_vitals (
    id               BIGSERIAL        PRIMARY KEY,
    received_at      TIMESTAMPTZ      NOT NULL DEFAULT now(),
    device_pseudonym TEXT             NOT NULL,
    metric           TEXT             NOT NULL CHECK (metric IN ('LCP', 'FID', 'CLS', 'INP', 'TTFB')),
    value            DOUBLE PRECISION NOT NULL,
    route            TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS telemetry_events_received_at_idx
    ON telemetry_events (received_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS telemetry_events_kind_received_idx
    ON telemetry_events (event_kind, received_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS telemetry_events_pseudonym_idx
    ON telemetry_events (device_pseudonym);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS telemetry_web_vitals_received_at_idx
    ON telemetry_web_vitals (received_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS telemetry_web_vitals_pseudonym_idx
    ON telemetry_web_vitals (device_pseudonym);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS telemetry_web_vitals;
DROP TABLE IF EXISTS telemetry_events;
-- +goose StatementEnd
