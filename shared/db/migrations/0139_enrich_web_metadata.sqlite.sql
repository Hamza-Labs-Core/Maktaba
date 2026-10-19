-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0139 (Epic 26 / Story 26.5, stub audit #4): the
-- metadata provider behind "Find metadata" and "Re-enrich". Arrays and JSONB are JSON
-- text, booleans 0/1, TIMESTAMPTZ TEXT.
--
--   enrich_jobs.query / year   a manual search's words and year (Find metadata)
--   enrich_jobs.origin         'manual' | 'reenrich' | 'auto'
--   enrich_usage               spend per UTC day and kind, against rules.enrich's caps
--   job_stages 'enrich'        the engine stage that runs enrich_jobs (home only)
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
    day        TEXT    NOT NULL,
    kind       TEXT    NOT NULL CHECK (kind IN ('search', 'page', 'llm')),
    calls      INTEGER NOT NULL DEFAULT 0,
    usd        REAL    NOT NULL DEFAULT 0,
    updated_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (day, kind)
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement)
VALUES
    ('enrich', 'Find metadata', '["video"]', 'lookup', 'media', 12, 1, 0, 0, 3, '[]', '[]', '[]', 0, 0, 0, 'auto', 'home')
ON CONFLICT (stage) DO NOTHING;
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
