-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0088 (plan 06-11 section 10.2, workstream A) - jobs act on a subject.
--
-- A job now acts on (subject_kind, subject_id): a video, a library, a
-- series, a season or the single global catalog. The typed foreign key
-- columns (video_id, library_id, plus the new series_id and season_id) are
-- kept so ON DELETE CASCADE still reaps a deleted thing's jobs; subject_id
-- duplicates whichever typed column the kind selects.
--
-- The migration also adds the change-driven columns (stage_version,
-- input_hash, output_hash, outcome, ...), the coalescing columns
-- (rerun_requested, enqueue_count, reasons, ...), the claim-time columns
-- and the processing_jobs_enqueue() function that Python and Go both call.
--
-- Rollout safety: the old INSERT paths (db/jobs.py before this change, and
-- the Go enqueuer) do not know the subject columns. A BEFORE INSERT trigger
-- fills them from video_id and library_id, so old and new writers coexist
-- until slot 0091 drops the legacy unique indexes.
--
-- This file runs outside a transaction because CREATE INDEX CONCURRENTLY
-- cannot run inside one. Every statement is individually idempotent.
--
CREATE TABLE IF NOT EXISTS series_seasons (
    id                UUID PRIMARY KEY,
    series_id         UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    library_id        UUID REFERENCES libraries(id) ON DELETE CASCADE,
    season            INT  NOT NULL,
    kind              TEXT NOT NULL CHECK (kind IN ('episodic','ongoing','short-form','clips')),
    name              TEXT NOT NULL,
    first_air         DATE,
    last_air          DATE,
    median_duration_s REAL,
    episode_count     INT  NOT NULL DEFAULT 0,
    membership_hash   TEXT NOT NULL,
    detector_version  INT  NOT NULL,
    metadata          JSONB NOT NULL DEFAULT '{}'::jsonb,
    retired_at        TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (series_id, season)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS series_seasons_library_idx
    ON series_seasons (library_id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS subject_kind TEXT NOT NULL DEFAULT 'video';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS subject_id UUID;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS series_id UUID REFERENCES series(id) ON DELETE CASCADE;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS season_id UUID REFERENCES series_seasons(id) ON DELETE CASCADE;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS stage_version INT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS input_hash TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS output_hash TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS outcome TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS output_summary JSONB;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS force BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS force_cascade BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS reasons JSONB NOT NULL DEFAULT '[]'::jsonb;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS enqueue_count INT NOT NULL DEFAULT 1;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS triggered_by BIGINT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS triggered_by_stage TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS rerun_requested BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS rerun_force BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS rerun_reasons JSONB NOT NULL DEFAULT '[]'::jsonb;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS propagated_at TIMESTAMPTZ;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS error_cause TEXT;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS progress JSONB;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
-- Rollout bridge: a legacy INSERT that only knows video_id / library_id gets
-- its subject columns filled here. New writers set them explicitly and the
-- trigger leaves them alone.
CREATE OR REPLACE FUNCTION processing_jobs_fill_subject() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.subject_id IS NULL THEN
        IF NEW.video_id IS NOT NULL THEN
            NEW.subject_kind := 'video';
            NEW.subject_id := NEW.video_id;
        ELSIF NEW.library_id IS NOT NULL THEN
            NEW.subject_kind := 'library';
            NEW.subject_id := NEW.library_id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER processing_jobs_fill_subject_trg
    BEFORE INSERT ON processing_jobs
    FOR EACH ROW
    EXECUTE FUNCTION processing_jobs_fill_subject();
-- +goose StatementEnd

-- +goose StatementBegin
-- Backfill (one time; the queue table is bounded). Scan rows are library
-- subjects, everything else is a video subject.
UPDATE processing_jobs
   SET subject_kind = 'library', subject_id = library_id
 WHERE stage = 'scan' AND subject_id IS NULL AND library_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE processing_jobs
   SET subject_id = video_id
 WHERE subject_kind = 'video' AND subject_id IS NULL AND video_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Denormalised library filter for per-video rows (the libraries table reads it).
UPDATE processing_jobs pj
   SET library_id = v.library_id
  FROM videos v
 WHERE pj.video_id = v.id AND pj.library_id IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- History is not re-propagated: mark finished rows as already propagated.
UPDATE processing_jobs
   SET outcome = 'changed', propagated_at = finished_at
 WHERE state = 'done' AND outcome IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP CONSTRAINT IF EXISTS processing_jobs_scope_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'processing_jobs_subject_chk'
    ) THEN
        ALTER TABLE processing_jobs
            ADD CONSTRAINT processing_jobs_subject_chk CHECK (
                subject_id IS NOT NULL AND (
                    (subject_kind = 'video'   AND video_id   = subject_id)
                 OR (subject_kind = 'library' AND library_id = subject_id AND video_id IS NULL)
                 OR (subject_kind = 'series'  AND series_id  = subject_id AND video_id IS NULL)
                 OR (subject_kind = 'season'  AND season_id  = subject_id AND video_id IS NULL)
                 OR (subject_kind = 'catalog'
                     AND subject_id = '00000000-0000-0000-0000-00000000ca7a'::uuid
                     AND video_id IS NULL)
                )
            ) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs VALIDATE CONSTRAINT processing_jobs_subject_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'processing_jobs_outcome_chk'
    ) THEN
        ALTER TABLE processing_jobs
            ADD CONSTRAINT processing_jobs_outcome_chk CHECK (
                outcome IS NULL OR outcome IN (
                    'changed', 'unchanged', 'skipped_unchanged', 'awaiting_review',
                    'waiting_inputs', 'imported', 'superseded'
                )
            ) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs VALIDATE CONSTRAINT processing_jobs_outcome_chk;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_one_live_per_subject_stage
    ON processing_jobs (subject_kind, subject_id, stage)
    WHERE state IN ('pending', 'claimed', 'running', 'resuming', 'paused');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_subject_history_idx
    ON processing_jobs (subject_kind, subject_id, stage, id DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_stage_state_idx
    ON processing_jobs (stage, state, priority, id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_finished_idx
    ON processing_jobs (stage, finished_at)
    WHERE finished_at IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_failed_cause_idx
    ON processing_jobs (stage, error_cause)
    WHERE state = 'failed';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_unpropagated_idx
    ON processing_jobs (id)
    WHERE state = 'done' AND propagated_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_library_idx
    ON processing_jobs (library_id, stage, state);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_created_idx
    ON processing_jobs (created_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- New reasons go first; the list is capped at 20 entries.
CREATE OR REPLACE FUNCTION processing_jobs_reason_prepend(p_old JSONB, p_entry JSONB)
RETURNS JSONB LANGUAGE sql IMMUTABLE AS $$
    SELECT COALESCE(jsonb_agg(x.e ORDER BY x.n), '[]'::jsonb)
      FROM (
        SELECT t.e, t.n
          FROM jsonb_array_elements(
                 jsonb_build_array(p_entry) || COALESCE(p_old, '[]'::jsonb)
               ) WITH ORDINALITY AS t(e, n)
         ORDER BY t.n
         LIMIT 20
      ) x
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The only payload key with defined merge behaviour is `seasons` (set union);
-- every other key keeps the existing value.
CREATE OR REPLACE FUNCTION processing_jobs_payload_merge(p_old JSONB, p_new JSONB)
RETURNS JSONB LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    merged JSONB;
BEGIN
    IF p_old IS NULL THEN
        RETURN p_new;
    END IF;
    IF p_new IS NULL OR jsonb_typeof(p_new) <> 'object' OR jsonb_typeof(p_old) <> 'object'
       OR NOT (p_new ? 'seasons') THEN
        RETURN p_old;
    END IF;
    SELECT COALESCE(jsonb_agg(DISTINCT s), '[]'::jsonb) INTO merged
      FROM jsonb_array_elements(COALESCE(p_old -> 'seasons', '[]'::jsonb) || (p_new -> 'seasons')) AS s;
    RETURN p_old || jsonb_build_object('seasons', merged);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION processing_jobs_enqueue(
    p_subject_kind   TEXT,
    p_subject_id     UUID,
    p_stage          TEXT,
    p_priority       INT     DEFAULT NULL,
    p_force          BOOLEAN DEFAULT false,
    p_force_cascade  BOOLEAN DEFAULT false,
    p_reason         JSONB   DEFAULT '{}'::jsonb,
    p_triggered_by   BIGINT  DEFAULT NULL,
    p_payload        JSONB   DEFAULT NULL,
    p_max_attempts   INT     DEFAULT NULL
) RETURNS TABLE (job_id BIGINT, outcome TEXT)
LANGUAGE plpgsql AS $$
#variable_conflict use_column
DECLARE
    s            job_stages%ROWTYPE;
    v_library    UUID;
    v_video      UUID;
    v_series     UUID;
    v_season     UUID;
    v_entry      JSONB;
    v_from_stage TEXT;
    v_id         BIGINT;
    v_outcome    TEXT;
    v_catalog    CONSTANT UUID := '00000000-0000-0000-0000-00000000ca7a';
BEGIN
    SELECT * INTO s FROM job_stages WHERE stage = p_stage;
    IF NOT FOUND THEN
        RETURN QUERY SELECT NULL::BIGINT, 'unknown_stage'::TEXT;
        RETURN;
    END IF;
    IF NOT s.enabled THEN
        RETURN QUERY SELECT NULL::BIGINT, 'disabled'::TEXT;
        RETURN;
    END IF;
    IF NOT (p_subject_kind = ANY (s.subject_kinds)) THEN
        RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
        RETURN;
    END IF;

    IF p_subject_kind = 'video' THEN
        SELECT v.library_id INTO v_library FROM videos v WHERE v.id = p_subject_id;
        IF NOT FOUND THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
        v_video := p_subject_id;
    ELSIF p_subject_kind = 'season' THEN
        SELECT ss.library_id INTO v_library FROM series_seasons ss WHERE ss.id = p_subject_id;
        IF NOT FOUND THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
        v_season := p_subject_id;
    ELSIF p_subject_kind = 'series' THEN
        SELECT sr.library_id INTO v_library FROM series sr WHERE sr.id = p_subject_id;
        IF NOT FOUND THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
        v_series := p_subject_id;
    ELSIF p_subject_kind = 'library' THEN
        PERFORM 1 FROM libraries l WHERE l.id = p_subject_id;
        IF NOT FOUND THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
        v_library := p_subject_id;
    ELSIF p_subject_kind = 'catalog' THEN
        IF p_subject_id IS DISTINCT FROM v_catalog THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
    ELSE
        RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
        RETURN;
    END IF;

    v_entry := COALESCE(p_reason, '{}'::jsonb) || jsonb_build_object('at', to_jsonb(now()));
    v_from_stage := COALESCE(
        p_reason ->> 'from_stage',
        (SELECT pj0.stage FROM processing_jobs pj0 WHERE pj0.id = p_triggered_by)
    );

    INSERT INTO processing_jobs AS pj
        (subject_kind, subject_id, video_id, library_id, series_id, season_id,
         stage, state, priority, max_attempts, payload, not_before,
         force, force_cascade, reasons, triggered_by, triggered_by_stage)
    VALUES
        (p_subject_kind, p_subject_id, v_video, v_library, v_series, v_season,
         p_stage, 'pending', COALESCE(p_priority, 100),
         COALESCE(p_max_attempts, s.max_attempts), p_payload,
         CASE WHEN s.debounce_sec > 0
              THEN now() + make_interval(secs => s.debounce_sec) END,
         p_force, p_force_cascade, jsonb_build_array(v_entry), p_triggered_by, v_from_stage)
    ON CONFLICT (subject_kind, subject_id, stage)
       WHERE state IN ('pending', 'claimed', 'running', 'resuming', 'paused')
    DO UPDATE SET
        priority = CASE WHEN pj.state = 'pending'
                        THEN LEAST(pj.priority, EXCLUDED.priority) ELSE pj.priority END,
        force = pj.force OR (pj.state = 'pending' AND EXCLUDED.force),
        force_cascade = pj.force_cascade OR (pj.state = 'pending' AND EXCLUDED.force_cascade),
        reasons = CASE WHEN pj.state = 'pending'
                       THEN processing_jobs_reason_prepend(pj.reasons, v_entry)
                       ELSE pj.reasons END,
        payload = processing_jobs_payload_merge(pj.payload, EXCLUDED.payload),
        enqueue_count = pj.enqueue_count + 1,
        not_before = CASE WHEN pj.state = 'pending' AND s.debounce_sec > 0
                          THEN LEAST(now() + make_interval(secs => s.debounce_sec),
                                     pj.created_at + make_interval(secs => s.debounce_max_sec))
                          ELSE pj.not_before END,
        rerun_requested = pj.rerun_requested OR pj.state <> 'pending',
        rerun_force = pj.rerun_force
                      OR (pj.state <> 'pending' AND (EXCLUDED.force OR EXCLUDED.force_cascade)),
        rerun_reasons = CASE WHEN pj.state <> 'pending'
                             THEN processing_jobs_reason_prepend(pj.rerun_reasons, v_entry)
                             ELSE pj.rerun_reasons END
    RETURNING pj.id,
              CASE WHEN pj.xmax = 0 THEN 'inserted'
                   WHEN pj.state = 'pending' THEN 'coalesced'
                   ELSE 'rerun_requested' END
         INTO v_id, v_outcome;

    RETURN QUERY SELECT v_id, v_outcome;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Superset of the slot 0002 payload: existing listeners keep working.
CREATE OR REPLACE FUNCTION processing_jobs_notify_new() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(
        'jobs.new',
        json_build_object(
            'id',           NEW.id,
            'video_id',     NEW.video_id,
            'library_id',   NEW.library_id,
            'subject_kind', NEW.subject_kind,
            'subject_id',   NEW.subject_id,
            'stage',        NEW.stage,
            'priority',     NEW.priority
        )::text
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose NO TRANSACTION
-- +goose StatementBegin
DROP FUNCTION IF EXISTS processing_jobs_enqueue(TEXT, UUID, TEXT, INT, BOOLEAN, BOOLEAN, JSONB, BIGINT, JSONB, INT);
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS processing_jobs_payload_merge(JSONB, JSONB);
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS processing_jobs_reason_prepend(JSONB, JSONB);
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS processing_jobs_fill_subject_trg ON processing_jobs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS processing_jobs_fill_subject();
-- +goose StatementEnd

DROP INDEX IF EXISTS processing_jobs_created_idx;
DROP INDEX IF EXISTS processing_jobs_library_idx;
DROP INDEX IF EXISTS processing_jobs_unpropagated_idx;
DROP INDEX IF EXISTS processing_jobs_failed_cause_idx;
DROP INDEX IF EXISTS processing_jobs_finished_idx;
DROP INDEX IF EXISTS processing_jobs_stage_state_idx;
DROP INDEX IF EXISTS processing_jobs_subject_history_idx;
DROP INDEX IF EXISTS processing_jobs_one_live_per_subject_stage;

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP CONSTRAINT IF EXISTS processing_jobs_outcome_chk;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP CONSTRAINT IF EXISTS processing_jobs_subject_chk;
-- +goose StatementEnd

-- Down stops here: the added columns and series_seasons are left in place
-- (dev-only rollback; dropping them would discard history).
