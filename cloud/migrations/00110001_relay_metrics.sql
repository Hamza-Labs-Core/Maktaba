-- +goose Up
-- Relay anonymous analytics (Epic 30, Story 30.1).
--
-- AGGREGATE ONLY. These tables intentionally carry NO user id, NO server
-- id, and NO IP address — the relay brokers other households' traffic and
-- must not become a PII store (README D1). The only dimension is
-- `country`, derived once at the edge (CF-IPCountry) and stored without
-- the IP it came from (Story 30.2 / GDPR).
--
-- Counters and gauges share one additive row shape (README D4): a counter
-- stores its summed delta (`samples`=0); a gauge stores sum(value) over
-- `samples` observations, so the hourly average is sum_value/samples.
CREATE TABLE IF NOT EXISTS relay_metrics_raw (
    id          BIGSERIAL   PRIMARY KEY,
    bucket      TIMESTAMPTZ NOT NULL,            -- minute bucket (UTC)
    metric      TEXT        NOT NULL,            -- metrics.Metric* name
    country     TEXT        NOT NULL DEFAULT '', -- ISO-3166 alpha-2 or ''
    value       BIGINT      NOT NULL DEFAULT 0,
    samples     INTEGER     NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (bucket, metric, country)
);
CREATE INDEX IF NOT EXISTS idx_relay_metrics_raw_bucket ON relay_metrics_raw (bucket);

-- Hourly rollup, retained 90 days (Story 30.2). Re-runnable: the rollup
-- overwrites a given (hour, metric, country) so a replay is idempotent.
CREATE TABLE IF NOT EXISTS relay_metrics_hourly (
    hour        TIMESTAMPTZ NOT NULL,
    metric      TEXT        NOT NULL,
    country     TEXT        NOT NULL DEFAULT '',
    sum_value   BIGINT      NOT NULL DEFAULT 0,
    max_value   BIGINT      NOT NULL DEFAULT 0,
    samples     INTEGER     NOT NULL DEFAULT 0,
    PRIMARY KEY (hour, metric, country)
);
CREATE INDEX IF NOT EXISTS idx_relay_metrics_hourly_metric ON relay_metrics_hourly (metric, hour);

-- +goose Down
DROP TABLE IF EXISTS relay_metrics_hourly;
DROP TABLE IF EXISTS relay_metrics_raw;
