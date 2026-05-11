-- +goose Up
-- +goose StatementBegin
--
-- Slot 0049 (Epic 9 / Stories 9.9, 9.10, 9.18) — pipeline stage extensions.
--
-- Extends the `processing_jobs.stage` CHECK to include the four new
-- stage names that Epic 9 owns:
--
--   `topic_recluster` — Story 9.9 nightly k-means recluster.
--   `topic_assign`    — Story 9.9 per-video topic assignment after INDEXED.
--   `categorize`      — Story 9.10 content_type classifier.
--   `chapter_infer`   — Story 9.18 chapter inference from topic shifts.
--
-- SQLite cannot ALTER a CHECK constraint, so we rebuild the table in
-- place using the standard create-new / copy / drop-old / rename
-- pattern from https://sqlite.org/lang_altertable.html#otheralter.
--
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS processing_jobs__new (
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
        'subtitle_gen', 'index', 'thumbnail',
        'topic_recluster', 'topic_assign', 'categorize', 'chapter_infer'
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
INSERT INTO processing_jobs__new SELECT * FROM processing_jobs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS processing_jobs;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs__new RENAME TO processing_jobs;
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

-- +goose StatementBegin
PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
