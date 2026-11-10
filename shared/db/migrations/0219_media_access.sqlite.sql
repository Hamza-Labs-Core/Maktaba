-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0219 (Epic 59, story 59.03): the library's access and
-- the subtitles kept in the database for a library Maktaba may only read.
--
ALTER TABLE libraries ADD COLUMN IF NOT EXISTS access TEXT
    CHECK (access IS NULL OR access IN ('rw', 'ro', 'none'));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE libraries ADD COLUMN IF NOT EXISTS access_checked_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE libraries ADD COLUMN IF NOT EXISTS access_detail TEXT NOT NULL DEFAULT '{}';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE subtitle_files ADD COLUMN IF NOT EXISTS content TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
