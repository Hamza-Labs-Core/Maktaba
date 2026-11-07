-- +goose Up
-- Story 50.19: a home's relayed bytes this month by the mode they were relayed in: full quality,
-- pay-as-you-go at full quality, capped, and capped past fair use. The relay's meter adds to it
-- with each flush (in the same transaction as bandwidth_monthly), and decides each home's mode
-- from it.
CREATE TABLE IF NOT EXISTS relay_usage_monthly (
    server_id         UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    month             DATE        NOT NULL,
    bytes_full        BIGINT      NOT NULL DEFAULT 0,
    bytes_payg        BIGINT      NOT NULL DEFAULT 0,
    bytes_capped      BIGINT      NOT NULL DEFAULT 0,
    bytes_capped_slow BIGINT      NOT NULL DEFAULT 0,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, month)
);

-- +goose Down
DROP TABLE IF EXISTS relay_usage_monthly;
