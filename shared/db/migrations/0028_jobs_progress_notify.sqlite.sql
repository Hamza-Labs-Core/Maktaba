-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0008 (Story 6.3).
--
-- SQLite has no LISTEN/NOTIFY; the Python helpers
-- ``maktaba_pipeline.db.jobs_progress.tick_progress`` and
-- ``tick_heartbeat`` publish to the in-process ``PubsubBus`` after the
-- UPDATE commits, mirroring the Postgres trigger output exactly so the
-- WS consumer never branches on dialect.
--
-- This migration is a no-op for the schema. We keep the slot reserved
-- so the migration numbering stays aligned across dialects.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
