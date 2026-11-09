-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0218 (the in-process fallbacks, counted; story 54.43). On
-- SQLite every first-party stage runs in the worker, counted under the reason 'sqlite'.
--
CREATE TABLE IF NOT EXISTS stage_fallbacks (
    machine  TEXT    NOT NULL,
    stage    TEXT    NOT NULL,
    reason   TEXT    NOT NULL,
    count    INTEGER NOT NULL DEFAULT 0,
    first_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (machine, stage, reason)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS stage_fallbacks;
-- +goose StatementEnd
