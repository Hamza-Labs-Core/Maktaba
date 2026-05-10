-- +goose Up
-- +goose StatementBegin
-- SQLite has no PL/pgSQL and no LISTEN/NOTIFY. The Python helper in
-- :mod:`maktaba_pipeline.stt.segment_commit` issues the equivalent
-- INSERT + UPDATE in a single explicit transaction and publishes on
-- the in-process pubsub bus. This file exists so the migration slot
-- is fully populated; the schema delta is empty on SQLite.
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
