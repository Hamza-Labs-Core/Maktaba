-- +goose Up
-- Epic 53 stories 53.14 and 53.16: what Maktaba keeps of a release's dSYMs, to read the Apple
-- apps' crash frames (a binary and an offset) as functions, files and lines when the service
-- admin opens a forwarded report. One row per binary and architecture, keyed by the binary's
-- UUID: its functions' ranges and its line table, gzip'd (cloud/internal/symbols). Uploaded
-- by `maktaba-symbols upload` with the symbols token; no user data.
CREATE TABLE IF NOT EXISTS symbol_tables (
    debug_id     TEXT        PRIMARY KEY,
    code_file    TEXT        NOT NULL DEFAULT '',
    arch         TEXT        NOT NULL DEFAULT '',
    version      TEXT        NOT NULL DEFAULT '',
    uploaded_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    size         INTEGER     NOT NULL,
    data         BYTEA       NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_symbol_tables_uploaded ON symbol_tables (uploaded_at DESC);

-- +goose Down
DROP TABLE IF EXISTS symbol_tables;
