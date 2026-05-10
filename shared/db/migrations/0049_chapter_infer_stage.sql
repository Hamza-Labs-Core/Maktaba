-- +goose Up
-- +goose StatementBegin
--
-- Slot 0049 (Epic 9 / Stories 9.9, 9.10, 9.18) — pipeline stage extensions.
--
-- Adds the four Epic-9 stage names to the canonical Epic 6 CHECK
-- constraint. Per `specs/epics/09-library-management/README.md`:
--
--   `topic_recluster` — Story 9.9 nightly k-means recluster.
--   `topic_assign`    — Story 9.9 per-video topic assignment after INDEXED.
--   `categorize`      — Story 9.10 content_type classifier.
--   `chapter_infer`   — Story 9.18 chapter inference from topic shifts.
--
-- This migration ships as a separate ALTER sequenced after Epic 6's
-- last migration; it deliberately does NOT edit Epic 6's
-- `0002_processing_jobs.sql`.
--
ALTER TABLE processing_jobs DROP CONSTRAINT IF EXISTS processing_jobs_stage_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs ADD CONSTRAINT processing_jobs_stage_check
    CHECK (stage IN (
        'scan', 'probe', 'extract', 'transcribe',
        'subtitle_gen', 'index', 'thumbnail',
        'topic_recluster', 'topic_assign', 'categorize', 'chapter_infer'
    ));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE processing_jobs DROP CONSTRAINT IF EXISTS processing_jobs_stage_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs ADD CONSTRAINT processing_jobs_stage_check
    CHECK (stage IN (
        'scan', 'probe', 'extract', 'transcribe',
        'subtitle_gen', 'index', 'thumbnail'
    ));
-- +goose StatementEnd
