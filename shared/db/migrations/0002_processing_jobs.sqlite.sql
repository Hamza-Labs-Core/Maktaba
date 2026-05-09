-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0002 — `processing_jobs` foundation
-- table. The Postgres canonical lives in `0002_processing_jobs.sql`;
-- this file follows architecture.md §8.0's preamble of type swaps:
--
--   UUID         → TEXT
--   TIMESTAMPTZ  → TEXT (ISO-8601)
--   BIGSERIAL    → INTEGER PRIMARY KEY AUTOINCREMENT
--   BOOLEAN      → INTEGER (0/1)
--   JSONB        → TEXT
--
-- SQLite has no LISTEN/NOTIFY, so the trigger from the Postgres file
-- is intentionally absent here — the Python helper publishes manually
-- on the in-process PubsubBus after a successful INSERT commits.
--
-- SQLite supports partial indexes since 3.8.0; the boolean column uses
-- `WHERE pause_requested = 1` instead of `= true`.
--
CREATE TABLE IF NOT EXISTS processing_jobs (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id                 TEXT NOT NULL
                              REFERENCES videos(id) ON DELETE CASCADE,
    stage                    TEXT NOT NULL,
    state                    TEXT NOT NULL DEFAULT 'pending',
    priority                 INTEGER NOT NULL DEFAULT 100,
    attempts                 INTEGER NOT NULL DEFAULT 0,
    max_attempts             INTEGER NOT NULL DEFAULT 3,
    claimed_by               TEXT,
    claimed_at               TEXT,
    last_heartbeat_at        TEXT,
    not_before               TEXT,
    error                    TEXT,

    total_duration_seconds   REAL,
    processed_seconds        REAL NOT NULL DEFAULT 0,
    segments_completed       INTEGER NOT NULL DEFAULT 0,
    last_segment_end_sec     REAL NOT NULL DEFAULT 0,
    estimated_remaining_sec  REAL,
    realtime_factor          REAL,
    progress_updated_at      TEXT,

    pause_requested          INTEGER NOT NULL DEFAULT 0,
    cancel_requested         INTEGER NOT NULL DEFAULT 0,
    paused_at                TEXT,
    paused_at_sec            REAL,
    paused_reason            TEXT,
    resumed_at               TEXT,
    resume_count             INTEGER NOT NULL DEFAULT 0,

    metrics                  TEXT,
    payload                  TEXT,
    created_at               TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at              TEXT,

    CHECK (stage IN (
        'scan', 'probe', 'extract', 'transcribe',
        'subtitle_gen', 'index', 'thumbnail'
    )),
    CHECK (state IN (
        'pending', 'claimed', 'running', 'paused',
        'resuming', 'done', 'failed', 'cancelled'
    )),
    CHECK (priority >= 0),
    CHECK (attempts >= 0 AND attempts <= max_attempts + 1),
    CHECK (
        last_segment_end_sec >= 0
        AND last_segment_end_sec <= COALESCE(total_duration_seconds,
                                             last_segment_end_sec)
    ),
    CHECK (pause_requested IN (0, 1)),
    CHECK (cancel_requested IN (0, 1))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_claim_idx
    ON processing_jobs (state, priority, not_before);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_video_stage_idx
    ON processing_jobs (video_id, stage);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_reaper_idx
    ON processing_jobs (state, last_heartbeat_at)
    WHERE state IN ('claimed', 'running', 'resuming');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_pause_pending_idx
    ON processing_jobs (pause_requested)
    WHERE pause_requested = 1;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS processing_jobs_one_live_per_video_stage
    ON processing_jobs (video_id, stage)
    WHERE state IN ('pending', 'claimed', 'running', 'resuming', 'paused');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS processing_jobs_one_live_per_video_stage;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS processing_jobs_pause_pending_idx;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS processing_jobs_reaper_idx;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS processing_jobs_video_stage_idx;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS processing_jobs_claim_idx;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS processing_jobs;
-- +goose StatementEnd
