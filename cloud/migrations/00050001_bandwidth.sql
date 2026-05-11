-- +goose Up
-- Bandwidth metering. We store raw 5-minute samples for the current
-- month plus rolled-up monthly buckets for billing. The hot read path
-- (current-month totals) hits Redis; this table is the durable source
-- of truth that survives Redis flush.
CREATE TABLE IF NOT EXISTS bandwidth_samples (
    server_id     UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    bucket_start  TIMESTAMPTZ NOT NULL,
    bytes_in      BIGINT      NOT NULL DEFAULT 0,
    bytes_out     BIGINT      NOT NULL DEFAULT 0,
    PRIMARY KEY (server_id, bucket_start)
);
CREATE INDEX idx_bandwidth_samples_bucket ON bandwidth_samples (bucket_start);

CREATE TABLE IF NOT EXISTS bandwidth_monthly (
    server_id     UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    month         DATE        NOT NULL,
    bytes_in      BIGINT      NOT NULL DEFAULT 0,
    bytes_out     BIGINT      NOT NULL DEFAULT 0,
    over_limit_at TIMESTAMPTZ,
    PRIMARY KEY (server_id, month)
);

-- +goose Down
DROP TABLE IF EXISTS bandwidth_monthly;
DROP TABLE IF EXISTS bandwidth_samples;
