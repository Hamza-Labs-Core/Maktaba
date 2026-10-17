-- +goose Up
-- +goose StatementBegin
--
-- Slot 0087 (plan 06-11 section 10.1, workstream A) - stage registry.
--
-- Replaces the hand-maintained stage CHECK constraints on processing_jobs
-- with a foreign key to a registry table, and fixes two defects:
--
--   F6  Three stage CHECKs coexist on processing_jobs
--       (processing_jobs_stage_chk from 0002, processing_jobs_stage_valid
--       from 0004 and processing_jobs_stage_check from 0049). Slot 0049
--       dropped a constraint that never existed and added a wider one, so
--       the two narrow ones still rejected topic_recluster, topic_assign,
--       categorize and chapter_infer. All three are dropped here and the
--       column is guarded by processing_jobs_stage_fk instead, so a new
--       stage never needs another CHECK migration.
--   F7  The Go control handlers write processing_jobs.updated_at, a column
--       that did not exist. It is added here and kept current by a
--       BEFORE UPDATE trigger.
--
-- worker_pools holds the resource classes (plan section 5); job_stages is
-- upserted from the Python stage graph at worker start. The seed below
-- carries every stage the graph defines so the foreign key validates
-- against existing rows; the graph sync never overwrites the operator
-- columns (enabled, paused, write_mode, ...).
--
-- Every statement is idempotent so a half-applied slot can be re-run.
--
CREATE TABLE IF NOT EXISTS worker_pools (
    pool          TEXT PRIMARY KEY CHECK (pool ~ '^[a-z][a-z0-9_]{1,31}$'),
    label         TEXT NOT NULL,
    scope         TEXT NOT NULL CHECK (scope IN ('global','per_worker')),
    size          INT  CHECK (size IS NULL OR size BETWEEN 0 AND 64),
    default_size  INT,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by    TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO worker_pools (pool, label, scope, size, default_size) VALUES
    ('nas_read', 'NAS reads', 'global', 2, 2),
    ('cpu', 'CPU', 'per_worker', NULL, NULL),
    ('gpu', 'GPU', 'per_worker', 1, 1),
    ('network_plex', 'Plex', 'global', 2, 2),
    ('network_tv', 'dizqueTV', 'global', 1, 1),
    ('network_yt', 'YouTube', 'global', 1, 1)
ON CONFLICT (pool) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS job_stages (
    stage            TEXT PRIMARY KEY CHECK (stage ~ '^[a-z][a-z0-9_]{1,39}$'),
    label            TEXT NOT NULL,
    subject_kinds    TEXT[] NOT NULL,
    pool             TEXT NOT NULL REFERENCES worker_pools(pool),
    lane             TEXT NOT NULL CHECK (lane IN ('sources','episodes','seasons','channels','media')),
    lane_order       INT  NOT NULL DEFAULT 0,
    version          INT  NOT NULL DEFAULT 1,
    debounce_sec     INT  NOT NULL DEFAULT 0,
    debounce_max_sec INT  NOT NULL DEFAULT 0,
    max_attempts     INT  NOT NULL DEFAULT 3,
    edges            JSONB NOT NULL DEFAULT '[]',
    may_request      TEXT[] NOT NULL DEFAULT '{}',
    entry_for        TEXT[] NOT NULL DEFAULT '{}',
    legacy           BOOLEAN NOT NULL DEFAULT false,
    hidden           BOOLEAN NOT NULL DEFAULT false,
    external_write   BOOLEAN NOT NULL DEFAULT false,
    extension        TEXT,
    enabled          BOOLEAN NOT NULL DEFAULT true,
    disabled_reason  TEXT,
    paused           BOOLEAN NOT NULL DEFAULT false,
    paused_at        TIMESTAMPTZ,
    paused_by        TEXT,
    write_mode       TEXT NOT NULL DEFAULT 'auto' CHECK (write_mode IN ('auto','review')),
    graph_synced_at  TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode)
VALUES
    ('youtube_sync', 'YouTube listing', ARRAY['library']::text[], 'network_yt', 'sources', 0, 1, 300, 3600, 3, '[{"to":"link","map":"videos_of","when":"output_changed","legacy":false},{"to":"detect","map":"same","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], ARRAY['library']::text[], false, false, false, 'auto'),
    ('probe', 'Length', ARRAY['video']::text[], 'nas_read', 'episodes', 1, 1, 0, 0, 3, '[{"to":"thumb_hash","map":"same","when":"output_changed","legacy":false},{"to":"link","map":"same","when":"output_changed","legacy":false},{"to":"detect","map":"library_of","when":"output_changed","legacy":false},{"to":"extract","map":"same","when":"output_changed","legacy":true}]'::jsonb, '{}'::text[], '{}'::text[], true, false, false, 'auto'),
    ('thumb_hash', 'Thumbnail', ARRAY['video']::text[], 'nas_read', 'episodes', 2, 1, 0, 0, 3, '[{"to":"detect","map":"library_of","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto'),
    ('link', 'Link', ARRAY['video']::text[], 'cpu', 'episodes', 3, 1, 0, 0, 3, '[{"to":"detect","map":"library_of","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto'),
    ('fingerprint', 'Fingerprint', ARRAY['video']::text[], 'nas_read', 'episodes', 4, 101, 0, 0, 3, '[{"to":"detect","map":"library_of","when":"output_changed","legacy":false},{"to":"fp_gate","map":"payload_seasons","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto'),
    ('style_sample', 'Style sample', ARRAY['video']::text[], 'nas_read', 'episodes', 5, 1, 0, 0, 3, '[{"to":"rate_style","map":"payload_seasons","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto'),
    ('detect', 'Place episodes', ARRAY['library']::text[], 'cpu', 'seasons', 1, 2, 30, 300, 3, '[{"to":"tag","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"rate_style","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"tag","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]'::jsonb, ARRAY['fingerprint','fp_gate']::text[], ARRAY['library']::text[], false, false, false, 'auto'),
    ('fp_gate', 'Samples to candidates', ARRAY['season']::text[], 'cpu', 'seasons', 1, 101, 10, 120, 3, '[]'::jsonb, ARRAY['fingerprint']::text[], '{}'::text[], false, true, false, 'auto'),
    ('tag', 'Tag topics', ARRAY['season','library']::text[], 'cpu', 'seasons', 2, 1, 0, 0, 3, '[{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], ARRAY['season']::text[], false, false, false, 'auto'),
    ('rate_style', 'Rate style', ARRAY['season']::text[], 'cpu', 'seasons', 3, 1, 10, 120, 3, '[{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]'::jsonb, ARRAY['style_sample']::text[], ARRAY['season']::text[], false, false, false, 'auto'),
    ('plan_channels', 'Plan channels', ARRAY['catalog']::text[], 'cpu', 'channels', 1, 1, 60, 900, 3, '[{"to":"write_channels","map":"same","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], ARRAY['catalog']::text[], false, false, false, 'auto'),
    ('write_channels', 'Write channels', ARRAY['catalog']::text[], 'cpu', 'channels', 2, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto'),
    ('scan', 'Scan folders', ARRAY['library']::text[], 'nas_read', 'media', 0, 1, 0, 0, 3, '[{"to":"probe","map":"videos_of","when":"output_changed","legacy":true}]'::jsonb, '{}'::text[], '{}'::text[], true, false, false, 'auto'),
    ('extract', 'Extract audio', ARRAY['video']::text[], 'nas_read', 'media', 1, 1, 0, 0, 3, '[{"to":"transcribe","map":"same","when":"output_changed","legacy":true}]'::jsonb, '{}'::text[], '{}'::text[], true, false, false, 'auto'),
    ('transcribe', 'Transcribe', ARRAY['video']::text[], 'gpu', 'media', 2, 1, 0, 0, 3, '[{"to":"subtitle_gen","map":"same","when":"output_changed","legacy":true},{"to":"index","map":"same","when":"output_changed","legacy":true}]'::jsonb, '{}'::text[], '{}'::text[], true, false, false, 'auto'),
    ('subtitle_gen', 'Subtitles', ARRAY['video']::text[], 'cpu', 'media', 3, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], true, false, false, 'auto'),
    ('index', 'Index', ARRAY['video']::text[], 'cpu', 'media', 4, 1, 0, 0, 3, '[{"to":"thumbnail","map":"same","when":"output_changed","legacy":true}]'::jsonb, '{}'::text[], '{}'::text[], true, false, false, 'auto'),
    ('thumbnail', 'Poster and sprites', ARRAY['video']::text[], 'nas_read', 'media', 5, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], true, false, false, 'auto'),
    ('topic_recluster', 'Recluster topics', ARRAY['library']::text[], 'cpu', 'media', 6, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], true, false, false, 'auto'),
    ('topic_assign', 'Assign topics', ARRAY['video']::text[], 'cpu', 'media', 7, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], true, false, false, 'auto'),
    ('categorize', 'Categorize', ARRAY['video']::text[], 'cpu', 'media', 8, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], true, false, false, 'auto'),
    ('chapter_infer', 'Infer chapters', ARRAY['video']::text[], 'cpu', 'media', 9, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], true, false, false, 'auto')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP CONSTRAINT IF EXISTS processing_jobs_stage_chk;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP CONSTRAINT IF EXISTS processing_jobs_stage_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP CONSTRAINT IF EXISTS processing_jobs_stage_check;
-- +goose StatementEnd

-- +goose StatementBegin
-- No ADD CONSTRAINT IF NOT EXISTS in Postgres; guard through pg_constraint.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'processing_jobs_stage_fk'
    ) THEN
        ALTER TABLE processing_jobs
            ADD CONSTRAINT processing_jobs_stage_fk
            FOREIGN KEY (stage) REFERENCES job_stages(stage) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs VALIDATE CONSTRAINT processing_jobs_stage_fk;
-- +goose StatementEnd

-- +goose StatementBegin
-- now() is not volatile, so Postgres 11+ stores a fast default: no rewrite.
ALTER TABLE processing_jobs
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION processing_jobs_touch() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER processing_jobs_touch_trg
    BEFORE UPDATE ON processing_jobs
    FOR EACH ROW
    EXECUTE FUNCTION processing_jobs_touch();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS processing_jobs_touch_trg ON processing_jobs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS processing_jobs_touch();
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP COLUMN IF EXISTS updated_at;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP CONSTRAINT IF EXISTS processing_jobs_stage_fk;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS job_stages;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS worker_pools;
-- +goose StatementEnd
