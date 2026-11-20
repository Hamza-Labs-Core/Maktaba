-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0233 (Epic 49 story 49.6): what a keyword search document was built from, for the
-- keyword_index stage's planner. A document records the stage version it was built by
-- (doc_version) and its transcript's revision (transcript_revision, bumped by every edit:
-- slot 0187). The planner queues the videos whose document is missing, is older than the
-- stage's version, or was built from another transcript, an older revision of it, or
-- another title. It finds them with one query, without queueing every video to let each
-- job's fingerprint decide.
--
-- video_search_docs is new (slot 0232) and empty until the stage fills it: a column with a
-- constant default adds no rewrite.
--
ALTER TABLE video_search_docs ADD COLUMN IF NOT EXISTS doc_version integer NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE video_search_docs ADD COLUMN IF NOT EXISTS transcript_revision integer NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE video_search_docs DROP COLUMN IF EXISTS transcript_revision;
ALTER TABLE video_search_docs DROP COLUMN IF EXISTS doc_version;
-- +goose StatementEnd
