-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0111 (plan 06-11, mediaintel parity): TIMESTAMPTZ and JSONB
-- become TEXT.
--
CREATE TABLE IF NOT EXISTS channel_audits (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    plan_hash    TEXT,
    window_days  INTEGER NOT NULL,
    channels     INTEGER NOT NULL,
    html_path    TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS channel_audit_channels (
    audit_id      INTEGER NOT NULL REFERENCES channel_audits(id) ON DELETE CASCADE,
    number        INTEGER NOT NULL,
    name          TEXT    NOT NULL,
    theme         TEXT,
    hours         REAL    NOT NULL,
    shares        TEXT    NOT NULL,
    filler_clips  INTEGER NOT NULL DEFAULT 0,
    filler_share  TEXT,
    detail        TEXT    NOT NULL,
    PRIMARY KEY (audit_id, number)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS channel_audits_created_idx ON channel_audits (created_at DESC);
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
