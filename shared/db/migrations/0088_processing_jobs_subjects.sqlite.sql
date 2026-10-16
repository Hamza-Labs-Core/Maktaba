-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0088 (plan 06-11 section 10.2).
--
-- SQLite cannot drop NOT NULL, swap a CHECK or add a foreign key in place,
-- so this slot rebuilds processing_jobs with the standard create-new / copy /
-- drop-old / rename sequence (precedent: slots 0049 and 0083). The rebuilt
-- table:
--
--   * has a nullable video_id (closes the slot 0058 parity gap);
--   * has no stage CHECK (stage is a foreign key into job_stages, slot 0087);
--   * carries every new column of the Postgres slot plus updated_at;
--   * is copied with an explicit column list, never SELECT *, because the
--     physical column order changes.
--
-- Differences from the Postgres file, inherent to SQLite:
--   * subject_id is nullable and the pairing CHECK tolerates NULL; an AFTER
--     INSERT trigger fills it from video_id / library_id for writers that do
--     not set it. The unique index below therefore only guards rows whose
--     subject_id is known, and the legacy unique indexes (recreated here) keep
--     guarding old writers.
--   * processing_jobs_enqueue() does not exist; Python carries the same
--     semantics (engine/enqueue.py) and is held to the same golden tests.
--   * No NOTIFY; the Python helper publishes on the in-process bus.
--
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS series_seasons (
    id                TEXT PRIMARY KEY,
    series_id         TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    library_id        TEXT REFERENCES libraries(id) ON DELETE CASCADE,
    season            INTEGER NOT NULL,
    kind              TEXT NOT NULL CHECK (kind IN ('episodic','ongoing','short-form','clips')),
    name              TEXT NOT NULL,
    first_air         TEXT,
    last_air          TEXT,
    median_duration_s REAL,
    episode_count     INTEGER NOT NULL DEFAULT 0,
    membership_hash   TEXT NOT NULL,
    detector_version  INTEGER NOT NULL,
    metadata          TEXT NOT NULL DEFAULT '{}',
    retired_at        TEXT,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (series_id, season)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS series_seasons_library_idx ON series_seasons (library_id);
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS processing_jobs__new;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS processing_jobs__new (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id                 TEXT REFERENCES videos(id) ON DELETE CASCADE,
    library_id               TEXT REFERENCES libraries(id) ON DELETE CASCADE,
    stage                    TEXT NOT NULL REFERENCES job_stages(stage),
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

    subject_kind             TEXT NOT NULL DEFAULT 'video',
    subject_id               TEXT,
    series_id                TEXT REFERENCES series(id) ON DELETE CASCADE,
    season_id                TEXT REFERENCES series_seasons(id) ON DELETE CASCADE,
    stage_version            INTEGER,
    input_hash               TEXT,
    output_hash              TEXT,
    outcome                  TEXT,
    output_summary           TEXT,
    force                    INTEGER NOT NULL DEFAULT 0,
    force_cascade            INTEGER NOT NULL DEFAULT 0,
    reasons                  TEXT NOT NULL DEFAULT '[]',
    enqueue_count            INTEGER NOT NULL DEFAULT 1,
    triggered_by             INTEGER,
    triggered_by_stage       TEXT,
    rerun_requested          INTEGER NOT NULL DEFAULT 0,
    rerun_force              INTEGER NOT NULL DEFAULT 0,
    rerun_reasons            TEXT NOT NULL DEFAULT '[]',
    propagated_at            TEXT,
    error_cause              TEXT,
    progress                 TEXT,
    started_at               TEXT,
    updated_at               TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),

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
    CHECK (cancel_requested IN (0, 1)),
    CHECK (force IN (0, 1)),
    CHECK (force_cascade IN (0, 1)),
    CHECK (rerun_requested IN (0, 1)),
    CHECK (rerun_force IN (0, 1)),
    CHECK (subject_kind IN ('video', 'library', 'series', 'season', 'catalog')),
    CHECK (
        subject_id IS NULL OR
        (subject_kind = 'video'   AND video_id   = subject_id)
     OR (subject_kind = 'library' AND library_id = subject_id AND video_id IS NULL)
     OR (subject_kind = 'series'  AND series_id  = subject_id AND video_id IS NULL)
     OR (subject_kind = 'season'  AND season_id  = subject_id AND video_id IS NULL)
     OR (subject_kind = 'catalog'
         AND subject_id = '00000000-0000-0000-0000-00000000ca7a' AND video_id IS NULL)
    ),
    CHECK (
        outcome IS NULL OR outcome IN (
            'changed', 'unchanged', 'skipped_unchanged', 'awaiting_review',
            'waiting_inputs', 'imported', 'superseded'
        )
    )
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO processing_jobs__new (
    id, video_id, library_id, stage, state, priority, attempts, max_attempts,
    claimed_by, claimed_at, last_heartbeat_at, not_before, error,
    total_duration_seconds, processed_seconds, segments_completed,
    last_segment_end_sec, estimated_remaining_sec, realtime_factor,
    progress_updated_at, pause_requested, cancel_requested, paused_at,
    paused_at_sec, paused_reason, resumed_at, resume_count, metrics, payload,
    created_at, finished_at,
    subject_kind, subject_id, outcome, propagated_at
)
SELECT
    id, video_id, library_id, stage, state, priority, attempts, max_attempts,
    claimed_by, claimed_at, last_heartbeat_at, not_before, error,
    total_duration_seconds, processed_seconds, segments_completed,
    last_segment_end_sec, estimated_remaining_sec, realtime_factor,
    progress_updated_at, pause_requested, cancel_requested, paused_at,
    paused_at_sec, paused_reason, resumed_at, resume_count, metrics, payload,
    created_at, finished_at,
    CASE WHEN stage = 'scan' AND video_id IS NULL AND library_id IS NOT NULL
         THEN 'library' ELSE 'video' END,
    CASE WHEN stage = 'scan' AND video_id IS NULL AND library_id IS NOT NULL
         THEN library_id ELSE video_id END,
    CASE WHEN state = 'done' THEN 'changed' ELSE NULL END,
    CASE WHEN state = 'done' THEN finished_at ELSE NULL END
FROM processing_jobs;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE processing_jobs__new
   SET library_id = (SELECT v.library_id FROM videos v WHERE v.id = processing_jobs__new.video_id)
 WHERE library_id IS NULL AND video_id IS NOT NULL;
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
CREATE UNIQUE INDEX IF NOT EXISTS processing_jobs_one_live_scan_per_library
    ON processing_jobs (library_id, stage)
    WHERE stage = 'scan'
      AND state IN ('pending', 'claimed', 'running', 'resuming', 'paused');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS processing_jobs_one_live_per_subject_stage
    ON processing_jobs (subject_kind, subject_id, stage)
    WHERE state IN ('pending', 'claimed', 'running', 'resuming', 'paused');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_subject_history_idx
    ON processing_jobs (subject_kind, subject_id, stage, id DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_stage_state_idx
    ON processing_jobs (stage, state, priority, id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_finished_idx
    ON processing_jobs (stage, finished_at)
    WHERE finished_at IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_failed_cause_idx
    ON processing_jobs (stage, error_cause)
    WHERE state = 'failed';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_unpropagated_idx
    ON processing_jobs (id)
    WHERE state = 'done' AND propagated_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_library_idx
    ON processing_jobs (library_id, stage, state);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS processing_jobs_created_idx
    ON processing_jobs (created_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- Rollout bridge (see header): fill the subject for writers that do not set it.
CREATE TRIGGER IF NOT EXISTS processing_jobs_fill_subject_trg
AFTER INSERT ON processing_jobs
FOR EACH ROW WHEN NEW.subject_id IS NULL
BEGIN
    UPDATE processing_jobs
       SET subject_kind = CASE WHEN NEW.video_id IS NULL THEN 'library' ELSE 'video' END,
           subject_id   = COALESCE(NEW.video_id, NEW.library_id)
     WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
-- updated_at upkeep (slot 0087 owns this trigger on Postgres).
CREATE TRIGGER IF NOT EXISTS processing_jobs_touch_trg
AFTER UPDATE ON processing_jobs
FOR EACH ROW WHEN NEW.updated_at IS OLD.updated_at
BEGIN
    UPDATE processing_jobs
       SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
     WHERE id = NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS processing_jobs_touch_trg;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS processing_jobs_fill_subject_trg;
-- +goose StatementEnd
