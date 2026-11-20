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
-- It also seeds the stage's job_stages row, so a freshly migrated database knows the stage
-- before any worker syncs the graph (the API's hooks queue it).
--
-- video_search_docs is new (slot 0232) and empty until the stage fills it: a column with a
-- constant default adds no rewrite.
--
ALTER TABLE video_search_docs ADD COLUMN IF NOT EXISTS doc_version integer NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE video_search_docs ADD COLUMN IF NOT EXISTS transcript_revision integer NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
-- the keyword_index stage (Epic 49, story 49.6): video and library subjects, the cpu pool,
-- speech lane 5, on any machine; its edges are the pipeline's to write (its graph sync). The
-- Down leaves the row, as every stage seed does: the queue's rows name it.
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled, disabled_reason)
VALUES
    ('keyword_index', 'Keyword index', ARRAY['video', 'library']::text[], 'cpu', 'speech', 5, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'any', true, NULL)
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE video_search_docs DROP COLUMN IF EXISTS transcript_revision;
ALTER TABLE video_search_docs DROP COLUMN IF EXISTS doc_version;
-- +goose StatementEnd
