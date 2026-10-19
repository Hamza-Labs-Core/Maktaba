-- +goose Up
-- +goose StatementBegin
--
-- Slot 0139 (Epic 26 / Story 26.5, stub audit #4) - the metadata provider behind
-- "Find metadata" and "Re-enrich": no outside catalogue, only Maktaba's own intel
-- (transcripts, chapters, the book a series explains, classification, file and folder
-- names, sibling episodes) and, when configured, a web search read by a language model.
--
--   enrich_jobs.query / year   a manual search's words and year (Find metadata); NULL for
--                              a Re-enrich or an automatic job
--   enrich_jobs.origin         'manual' | 'reenrich' | 'auto' (what asked for the job)
--   enrich_usage               what the provider spent per UTC day and kind ('search',
--                              'page', 'llm'): calls and dollars, against the daily caps in
--                              rules.enrich
--   job_stages 'enrich'        the engine stage that runs enrich_jobs (video subject, home
--                              only: the search key is in home_secrets), so a Re-enrich
--                              shows in the control centre
--
-- Searches, pages and the model's answers are cached in web_metadata_cache (slot 0077).
--
ALTER TABLE enrich_jobs ADD COLUMN IF NOT EXISTS query TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE enrich_jobs ADD COLUMN IF NOT EXISTS year INTEGER;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE enrich_jobs ADD COLUMN IF NOT EXISTS origin TEXT NOT NULL DEFAULT 'auto';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS enrich_usage (
    day        TEXT             NOT NULL, -- UTC day, YYYY-MM-DD
    kind       TEXT             NOT NULL CHECK (kind IN ('search', 'page', 'llm')),
    calls      INTEGER          NOT NULL DEFAULT 0,
    usd        DOUBLE PRECISION NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ      NOT NULL DEFAULT now(),
    PRIMARY KEY (day, kind)
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement)
VALUES
    ('enrich', 'Find metadata', ARRAY['video']::text[], 'lookup', 'media', 12, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'home')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- the home's workers spend and read it; a machine never runs the stage
        REVOKE ALL ON enrich_usage FROM maktaba_worker;
        GRANT SELECT ON enrich_usage TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'enrich'
   AND NOT EXISTS (SELECT 1 FROM processing_jobs WHERE stage = 'enrich');
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS enrich_usage;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE enrich_jobs DROP COLUMN IF EXISTS origin;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE enrich_jobs DROP COLUMN IF EXISTS year;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE enrich_jobs DROP COLUMN IF EXISTS query;
-- +goose StatementEnd
