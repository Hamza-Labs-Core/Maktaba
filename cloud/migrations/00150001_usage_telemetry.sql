-- +goose Up
-- Epic 16 story 16.7: anonymous usage data from homes (docs/usage-data.md).
--
-- Every row is keyed by a random install id the home or app made at
-- opt-in. Nothing here links to a cloud account, a server, a user or an
-- IP address: there are no foreign keys and no address columns. A
-- "forget" batch deletes every row of an install id.

-- One report per home per UTC day; a resend of the same day replaces it.
CREATE TABLE IF NOT EXISTS telemetry_server_reports (
    install_id    TEXT        NOT NULL,
    day           DATE        NOT NULL,
    version       TEXT        NOT NULL,
    os            TEXT        NOT NULL,
    arch          TEXT        NOT NULL,
    install_kind  TEXT        NOT NULL,
    library_size  TEXT        NOT NULL,
    libraries     TEXT        NOT NULL,
    users         TEXT        NOT NULL,
    machines      TEXT        NOT NULL,
    apps          JSONB       NOT NULL DEFAULT '[]'::jsonb,
    features      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    received_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (install_id, day)
);
CREATE INDEX IF NOT EXISTS telemetry_server_reports_day_idx ON telemetry_server_reports (day);

-- One row per app install per UTC day it sent anything: the install's
-- app, platform and versions that day (installs, versions, platforms).
CREATE TABLE IF NOT EXISTS telemetry_app_installs (
    install_id   TEXT        NOT NULL,
    day          DATE        NOT NULL,
    app          TEXT        NOT NULL,
    platform     TEXT        NOT NULL,
    form_factor  TEXT        NOT NULL DEFAULT '',
    app_version  TEXT        NOT NULL,
    os_version   TEXT        NOT NULL DEFAULT '',
    locale       TEXT        NOT NULL DEFAULT '',
    PRIMARY KEY (install_id, day)
);
CREATE INDEX IF NOT EXISTS telemetry_app_installs_day_idx ON telemetry_app_installs (day);

-- App events (allow-listed kinds; error.uncaught goes to the error tables).
CREATE TABLE IF NOT EXISTS telemetry_app_events (
    id           BIGSERIAL   PRIMARY KEY,
    install_id   TEXT        NOT NULL,
    app          TEXT        NOT NULL,
    app_version  TEXT        NOT NULL,
    kind         TEXT        NOT NULL,
    payload      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    ts           TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS telemetry_app_events_ts_idx ON telemetry_app_events (ts);
CREATE INDEX IF NOT EXISTS telemetry_app_events_kind_ts_idx ON telemetry_app_events (kind, ts);
CREATE INDEX IF NOT EXISTS telemetry_app_events_install_idx ON telemetry_app_events (install_id);

-- Error groups: fingerprint = hash of source, app, the scrubbed message
-- and the first stack frame. The group holds the first message seen;
-- counts live in the occurrences.
CREATE TABLE IF NOT EXISTS telemetry_error_groups (
    fingerprint  TEXT        PRIMARY KEY,
    source       TEXT        NOT NULL CHECK (source IN ('server','app')),
    app          TEXT        NOT NULL,
    message      TEXT        NOT NULL,
    first_frame  TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Occurrences: per group, install, UTC day and version.
CREATE TABLE IF NOT EXISTS telemetry_error_occurrences (
    fingerprint  TEXT        NOT NULL REFERENCES telemetry_error_groups(fingerprint) ON DELETE CASCADE,
    install_id   TEXT        NOT NULL,
    day          DATE        NOT NULL,
    version      TEXT        NOT NULL,
    count        BIGINT      NOT NULL CHECK (count >= 0),
    first_seen   TIMESTAMPTZ NOT NULL,
    last_seen    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (fingerprint, install_id, day, version)
);
CREATE INDEX IF NOT EXISTS telemetry_error_occurrences_day_idx ON telemetry_error_occurrences (day);
CREATE INDEX IF NOT EXISTS telemetry_error_occurrences_install_idx ON telemetry_error_occurrences (install_id);

-- Web vitals (web app only).
CREATE TABLE IF NOT EXISTS telemetry_web_vitals (
    id           BIGSERIAL        PRIMARY KEY,
    install_id   TEXT             NOT NULL,
    app          TEXT             NOT NULL,
    app_version  TEXT             NOT NULL,
    metric       TEXT             NOT NULL,
    value        DOUBLE PRECISION NOT NULL,
    route        TEXT             NOT NULL DEFAULT '',
    received_at  TIMESTAMPTZ      NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS telemetry_web_vitals_metric_idx ON telemetry_web_vitals (metric, received_at);
CREATE INDEX IF NOT EXISTS telemetry_web_vitals_received_idx ON telemetry_web_vitals (received_at);
CREATE INDEX IF NOT EXISTS telemetry_web_vitals_install_idx ON telemetry_web_vitals (install_id);

-- +goose Down
DROP TABLE IF EXISTS telemetry_web_vitals;
DROP TABLE IF EXISTS telemetry_error_occurrences;
DROP TABLE IF EXISTS telemetry_error_groups;
DROP TABLE IF EXISTS telemetry_app_events;
DROP TABLE IF EXISTS telemetry_app_installs;
DROP TABLE IF EXISTS telemetry_server_reports;
