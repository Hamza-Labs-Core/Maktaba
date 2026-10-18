-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0107 (plan 06-11, mediaintel parity) - the channel audit: is what each dizqueTV channel
-- airs about the channel? One row per audit run, one per channel in it. The dizquetv_audit
-- stage also writes the same report as channel-audit.html beside the review pages.
--
CREATE TABLE IF NOT EXISTS channel_audits (
    id           BIGSERIAL PRIMARY KEY,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    plan_hash    TEXT,
    window_days  INT         NOT NULL,
    channels     INT         NOT NULL,
    html_path    TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS channel_audit_channels (
    audit_id      BIGINT  NOT NULL REFERENCES channel_audits(id) ON DELETE CASCADE,
    number        INT     NOT NULL,
    name          TEXT    NOT NULL,
    theme         TEXT,
    hours         REAL    NOT NULL,
    shares        JSONB   NOT NULL,
    filler_clips  INT     NOT NULL DEFAULT 0,
    filler_share  JSONB,
    detail        JSONB   NOT NULL,
    PRIMARY KEY (audit_id, number)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS channel_audits_created_idx ON channel_audits (created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS channel_audits_created_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS channel_audit_channels;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS channel_audits;
-- +goose StatementEnd
