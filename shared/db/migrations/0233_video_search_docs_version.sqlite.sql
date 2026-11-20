-- +goose Up
-- +goose StatementBegin
-- Slot 0233 (Epic 49 story 49.6): what a keyword search document was built from (the
-- keyword_index stage's version and its transcript's revision), as on Postgres. ADD COLUMN IF NOT
-- EXISTS is the API's SQLite migrator's (api/migrate_sqlite.go).
ALTER TABLE video_search_docs ADD COLUMN IF NOT EXISTS doc_version INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE video_search_docs ADD COLUMN IF NOT EXISTS transcript_revision INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE video_search_docs DROP COLUMN transcript_revision;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE video_search_docs DROP COLUMN doc_version;
-- +goose StatementEnd
