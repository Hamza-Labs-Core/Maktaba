-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0197 (Epic 54, stories 54.22-54.27): the per-stage switch.
-- Maktaba's first-party plugin runners need a Postgres home (the plugin API's statements
-- run under a Postgres role); on SQLite every stage stays 'inproc'.
--
ALTER TABLE job_stages ADD COLUMN IF NOT EXISTS impl TEXT NOT NULL DEFAULT 'inproc'
    CHECK (impl IN ('inproc', 'plugin'));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE job_stages ADD COLUMN IF NOT EXISTS flipping INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE job_stages ADD COLUMN IF NOT EXISTS impl_changed_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE job_stages ADD COLUMN IF NOT EXISTS impl_changed_by TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
