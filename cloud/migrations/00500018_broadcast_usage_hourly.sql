-- +goose Up
-- Story 50.28: viewer analytics and broadcast monitoring. Expand only: new tables, nothing the
-- running release reads changes.

-- one row per publisher, channel and UTC hour, rolled up hourly by the worker from the edge's
-- Analytics Engine events (broadcast_events) and kept 13 months. Aggregate only: no session,
-- address or viewer id. countries and devices count sessions that started in the hour (one play
-- each); quality is viewer-minutes per rung; errors_by_code and blocked count events. Metering's
-- truth is the edge's PublisherControl (50.20's broadcast_usage_windows), not this table.
CREATE TABLE IF NOT EXISTS broadcast_usage_hourly (
    publisher_id    TEXT        NOT NULL,
    channel_id      TEXT        NOT NULL,
    hour            TIMESTAMPTZ NOT NULL,
    viewer_minutes  DOUBLE PRECISION NOT NULL DEFAULT 0,
    sessions        DOUBLE PRECISION NOT NULL DEFAULT 0,
    peak_concurrent DOUBLE PRECISION NOT NULL DEFAULT 0,
    countries       JSONB       NOT NULL DEFAULT '{}',
    devices         JSONB       NOT NULL DEFAULT '{}',
    quality         JSONB       NOT NULL DEFAULT '{}',
    rebuffer_ratio  DOUBLE PRECISION NOT NULL DEFAULT 0,
    startup_ms_sum  DOUBLE PRECISION NOT NULL DEFAULT 0,
    errors          DOUBLE PRECISION NOT NULL DEFAULT 0,
    errors_by_code  JSONB       NOT NULL DEFAULT '{}',
    blocked         JSONB       NOT NULL DEFAULT '{}',
    segments        DOUBLE PRECISION NOT NULL DEFAULT 0,
    segment_bytes   DOUBLE PRECISION NOT NULL DEFAULT 0,
    rungs           SMALLINT    NOT NULL DEFAULT 0,
    rolled_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (publisher_id, channel_id, hour)
);
CREATE INDEX IF NOT EXISTS broadcast_usage_hourly_hour ON broadcast_usage_hourly (hour);

-- the viewers watching now, per publisher and channel: the worker replaces it every minute
CREATE TABLE IF NOT EXISTS broadcast_live (
    publisher_id TEXT        NOT NULL,
    channel_id   TEXT        NOT NULL,
    viewers      DOUBLE PRECISION NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (publisher_id, channel_id)
);

-- the monitoring state behind the maktaba_broadcast_* series (one row, id = 1): the worker's
-- minute poll writes it, every api node reads it for /metrics. rolled_through is the last hour
-- the rollup finished (it catches up from there); checked_day the last day cross-checked.
CREATE TABLE IF NOT EXISTS broadcast_monitor (
    id             SMALLINT    PRIMARY KEY CHECK (id = 1),
    snapshot       JSONB       NOT NULL DEFAULT '{}',
    polled_minute  TIMESTAMPTZ,
    rolled_through TIMESTAMPTZ,
    checked_day    DATE,
    revocations_at TIMESTAMPTZ,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO broadcast_monitor (id) VALUES (1) ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS broadcast_monitor;
DROP TABLE IF EXISTS broadcast_live;
DROP TABLE IF EXISTS broadcast_usage_hourly;
