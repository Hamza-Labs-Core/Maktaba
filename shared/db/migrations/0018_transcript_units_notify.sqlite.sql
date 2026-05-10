-- +goose Up
-- +goose StatementBegin
-- SQLite has no LISTEN/NOTIFY; the live indexer publishes via
-- the in-process pubsub bus in
-- `pipeline/src/maktaba_pipeline/db/pubsub.py`. This file is a
-- placeholder so the migration manifest stays parity-aligned across
-- engines.
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
