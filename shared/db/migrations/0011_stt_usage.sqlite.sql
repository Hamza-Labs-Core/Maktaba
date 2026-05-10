-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS stt_usage (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    library_id    TEXT    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    backend       TEXT    NOT NULL,
    period_yyyymm INTEGER NOT NULL,
    minutes       REAL    NOT NULL DEFAULT 0,
    est_usd       REAL    NOT NULL DEFAULT 0,
    updated_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (library_id, backend, period_yyyymm)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS stt_usage_lookup_idx
    ON stt_usage (library_id, period_yyyymm);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS stt_usage;
-- +goose StatementEnd
