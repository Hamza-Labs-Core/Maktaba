-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0011 (Story 3.4 / plan-03-04) — `stt_usage` ledger.
--
-- Records minutes consumed and (estimated) USD spent per backend per
-- library per calendar month. The OpenAI backend's per-claim budget
-- check sums the current month's `est_usd` and refuses to claim when
-- the projected total would exceed `library.settings.stt.backends.<name>.max_usd_per_month`.
--
CREATE TABLE IF NOT EXISTS stt_usage (
    id           BIGSERIAL    PRIMARY KEY,
    library_id   UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    backend      TEXT         NOT NULL,
    period_yyyymm INT         NOT NULL,
    minutes      REAL         NOT NULL DEFAULT 0,
    est_usd      REAL         NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (library_id, backend, period_yyyymm)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS stt_usage_lookup_idx
    ON stt_usage (library_id, period_yyyymm);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS stt_usage;
-- +goose StatementEnd
