-- +goose Up
-- +goose NO TRANSACTION
--
-- Slot 0002 (Story 6.1) — `processing_jobs` foundation table.
--
-- Mirrors architecture.md §7.1 verbatim and adds the `payload` JSONB
-- column from plan-06-01 §2.3 so per-stage options ride along with the
-- row instead of polluting the column list. Establishes:
--
--   * Stage CHECK constraint (canonical enum from Story 1.6)
--   * State CHECK constraint (8 states from §7.2)
--   * Priority/attempts/resume-offset CHECK constraints
--   * Four query-shape indexes (claim, video-stage, reaper, pause)
--   * Partial UNIQUE index that makes `enqueue` idempotent without a
--     SELECT-then-INSERT race window
--   * AFTER INSERT trigger that publishes `pg_notify('jobs.new', …)`
--     for the claim loop and the WS fan-out
--
-- Depends on slot 0001 (`videos` table). The FK is declared inline; if
-- 0001 is missing, this migration fails at apply time. Goose enforces
-- the ordering for us.
--
-- This file uses `+goose NO TRANSACTION` because Postgres `CREATE INDEX
-- CONCURRENTLY` cannot run inside a transaction (the project lint
-- requires CONCURRENTLY for every Postgres-targeted `CREATE INDEX`).
-- Every statement uses `IF [NOT] EXISTS` / `CREATE OR REPLACE` so a
-- partially-applied migration can be re-run cleanly.
--

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS processing_jobs (
    id                       BIGSERIAL PRIMARY KEY,
    video_id                 UUID NOT NULL
                              REFERENCES videos(id) ON DELETE CASCADE,
    stage                    TEXT NOT NULL,
    state                    TEXT NOT NULL DEFAULT 'pending',
    priority                 INT  NOT NULL DEFAULT 100,
    attempts                 INT  NOT NULL DEFAULT 0,
    max_attempts             INT  NOT NULL DEFAULT 3,
    claimed_by               TEXT,
    claimed_at               TIMESTAMPTZ,
    last_heartbeat_at        TIMESTAMPTZ,
    not_before               TIMESTAMPTZ,
    error                    TEXT,

    total_duration_seconds   REAL,
    processed_seconds        REAL NOT NULL DEFAULT 0,
    segments_completed       INT  NOT NULL DEFAULT 0,
    last_segment_end_sec     REAL NOT NULL DEFAULT 0,
    estimated_remaining_sec  REAL,
    realtime_factor          REAL,
    progress_updated_at      TIMESTAMPTZ,

    pause_requested          BOOLEAN NOT NULL DEFAULT false,
    cancel_requested         BOOLEAN NOT NULL DEFAULT false,
    paused_at                TIMESTAMPTZ,
    paused_at_sec            REAL,
    paused_reason            TEXT,
    resumed_at               TIMESTAMPTZ,
    resume_count             INT  NOT NULL DEFAULT 0,

    metrics                  JSONB,
    payload                  JSONB,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at              TIMESTAMPTZ,

    CONSTRAINT processing_jobs_stage_chk CHECK (
        stage IN (
            'scan', 'probe', 'extract', 'transcribe',
            'subtitle_gen', 'index', 'thumbnail'
        )
    ),

    CONSTRAINT processing_jobs_state_chk CHECK (
        state IN (
            'pending', 'claimed', 'running', 'paused',
            'resuming', 'done', 'failed', 'cancelled'
        )
    ),

    CONSTRAINT processing_jobs_priority_chk CHECK (priority >= 0),

    CONSTRAINT processing_jobs_attempts_chk CHECK (
        attempts >= 0 AND attempts <= max_attempts + 1
    ),

    CONSTRAINT processing_jobs_resume_offset_chk CHECK (
        last_segment_end_sec >= 0
        AND last_segment_end_sec <= COALESCE(total_duration_seconds,
                                             last_segment_end_sec)
    )
);
-- +goose StatementEnd

-- Claim index: state-priority-not_before is the exact filter the claim
-- loop's WHERE/ORDER BY uses (Story 6.2).
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_claim_idx
    ON processing_jobs (state, priority, not_before);

-- Per-video status: "what's pending for this video?" used by the API's
-- per-video status view and the scanner's "is anything in flight?" check.
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_video_stage_idx
    ON processing_jobs (video_id, stage);

-- Reaper partial index: only live-claim states matter for the stale-
-- heartbeat sweep (Story 6.6). Partial keeps it tiny — a 1M-row table
-- with 99% terminal entries indexes ~10K rows.
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_reaper_idx
    ON processing_jobs (state, last_heartbeat_at)
    WHERE state IN ('claimed', 'running', 'resuming');

-- Pause poller partial index: only rows that have asked to be paused
-- (Story 6.4).
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_pause_pending_idx
    ON processing_jobs (pause_requested)
    WHERE pause_requested = true;

-- Liveness uniqueness: at most one non-terminal job per (video, stage).
-- The unique partial index is what makes `enqueue` idempotent without a
-- SELECT-then-INSERT race window — concurrent inserts collide on this
-- index, the loser's INSERT becomes a no-op, and the caller falls back
-- to the existing live row's id.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_one_live_per_video_stage
    ON processing_jobs (video_id, stage)
    WHERE state IN ('pending', 'claimed', 'running', 'resuming', 'paused');

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION processing_jobs_notify_new() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(
        'jobs.new',
        json_build_object(
            'id',       NEW.id,
            'video_id', NEW.video_id,
            'stage',    NEW.stage,
            'priority', NEW.priority
        )::text
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER processing_jobs_notify_new_trg
    AFTER INSERT ON processing_jobs
    FOR EACH ROW
    EXECUTE FUNCTION processing_jobs_notify_new();
-- +goose StatementEnd

-- +goose Down
-- +goose NO TRANSACTION
-- +goose StatementBegin
DROP TRIGGER IF EXISTS processing_jobs_notify_new_trg ON processing_jobs;
-- +goose StatementEnd
-- +goose StatementBegin
DROP FUNCTION IF EXISTS processing_jobs_notify_new();
-- +goose StatementEnd
DROP INDEX IF EXISTS processing_jobs_one_live_per_video_stage;
DROP INDEX IF EXISTS processing_jobs_pause_pending_idx;
DROP INDEX IF EXISTS processing_jobs_reaper_idx;
DROP INDEX IF EXISTS processing_jobs_video_stage_idx;
DROP INDEX IF EXISTS processing_jobs_claim_idx;
DROP TABLE IF EXISTS processing_jobs;
