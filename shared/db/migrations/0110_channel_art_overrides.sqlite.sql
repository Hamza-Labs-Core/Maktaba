-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0110 (Epic 27, channel art): UUID and TIMESTAMPTZ become
-- TEXT, JSONB becomes TEXT.
--
CREATE TABLE IF NOT EXISTS channel_art_overrides (
    channel_id  TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('logo','watermark','loading')),
    path        TEXT,
    settings    TEXT,
    base        TEXT,
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (channel_id, kind)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS channel_art_overrides;
-- +goose StatementEnd
