-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0166 (Epic 47: file ledger and trash, duplicates, YouTube
-- sources and downloads, the convert pool and stages). UUIDs and timestamps are TEXT,
-- booleans 0/1, arrays and JSONB JSON text. No grants (SQLite has no roles).
--
-- SQLite cannot swap a CHECK in place: processing_jobs is rebuilt (create new, copy with an
-- explicit column list, drop old, rename; precedent slot 0088) so subject_kind may be
-- 'dup_group'. processing_jobs_enqueue() does not exist here: engine/enqueue.py resolves a
-- dup_group subject the same way.
--
CREATE TABLE IF NOT EXISTS file_actions (
    id           TEXT    PRIMARY KEY DEFAULT (lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' || substr('89ab', 1 + (abs(random()) % 4), 1) || substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6)))),
    library_id   TEXT    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    video_id     TEXT    REFERENCES videos(id) ON DELETE SET NULL,
    job_id       INTEGER,
    kind         TEXT    NOT NULL
                 CHECK (kind IN ('convert', 'dedup', 'youtube_fetch', 'purge', 'restore')),
    state        TEXT    NOT NULL DEFAULT 'planned'
                 CHECK (state IN ('planned', 'staged', 'verified', 'swapped', 'done',
                                  'rolled_back', 'failed')),
    old_path     TEXT,
    new_path     TEXT,
    trash_path   TEXT,
    old_hash     TEXT,
    new_hash     TEXT,
    old_size     INTEGER,
    new_size     INTEGER,
    verify       TEXT,
    error        TEXT,
    purge_after  TEXT,
    purged_at    TEXT,
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS file_actions_open_idx
    ON file_actions (state, updated_at)
    WHERE state IN ('planned', 'staged', 'verified', 'swapped');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS file_actions_purge_idx
    ON file_actions (library_id, purge_after)
    WHERE trash_path IS NOT NULL AND purged_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS file_actions_library_idx
    ON file_actions (library_id, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS file_actions_video_idx
    ON file_actions (video_id) WHERE video_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS video_copies (
    video_id    TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    path        TEXT    NOT NULL,
    size_bytes  INTEGER NOT NULL,
    mtime       TEXT,
    seen_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (video_id, path)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS video_copies_path_idx ON video_copies (path);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS duplicate_groups (
    id               TEXT    PRIMARY KEY DEFAULT (lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' || substr('89ab', 1 + (abs(random()) % 4), 1) || substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6)))),
    library_id       TEXT    REFERENCES libraries(id) ON DELETE CASCADE,
    tier             TEXT    NOT NULL CHECK (tier IN ('exact', 'same_video')),
    status           TEXT    NOT NULL DEFAULT 'open'
                     CHECK (status IN ('open', 'applied', 'dismissed', 'failed')),
    keeper_video_id  TEXT    REFERENCES videos(id) ON DELETE SET NULL,
    members_key      TEXT    NOT NULL,
    reason           TEXT,
    created_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    decided_by       TEXT,
    decided_at       TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS duplicate_groups_library_idx
    ON duplicate_groups (library_id, status, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS duplicate_groups_members_key_idx
    ON duplicate_groups (members_key);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS duplicate_members (
    group_id  TEXT    NOT NULL REFERENCES duplicate_groups(id) ON DELETE CASCADE,
    video_id  TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    path      TEXT    NOT NULL,
    is_copy   INTEGER NOT NULL DEFAULT 0,
    score     TEXT,
    PRIMARY KEY (group_id, video_id, path)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS duplicate_members_video_idx
    ON duplicate_members (video_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_sources (
    id               TEXT    PRIMARY KEY DEFAULT (lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' || substr('89ab', 1 + (abs(random()) % 4), 1) || substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6)))),
    library_id       TEXT    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    kind             TEXT    NOT NULL CHECK (kind IN ('channel', 'playlist')),
    url              TEXT    NOT NULL,
    external_id      TEXT    NOT NULL,
    folder           TEXT,
    mode             TEXT    NOT NULL DEFAULT 'new_and_backfill'
                     CHECK (mode IN ('off', 'new_and_backfill', 'new_only')),
    added_at         TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    options          TEXT    NOT NULL DEFAULT '{}',
    status           TEXT,
    error            TEXT,
    last_checked_at  TEXT,
    last_fetched_at  TEXT,
    created_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (library_id, kind, external_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_downloads (
    library_id     TEXT    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    youtube_id     TEXT    NOT NULL,
    source_id      TEXT    REFERENCES youtube_sources(id) ON DELETE SET NULL,
    state          TEXT    NOT NULL DEFAULT 'wanted'
                   CHECK (state IN ('wanted', 'downloading', 'done', 'skipped', 'gone',
                                    'failed')),
    backfill       INTEGER NOT NULL DEFAULT 0,
    reason         TEXT,
    video_id       TEXT    REFERENCES videos(id) ON DELETE SET NULL,
    attempts       INTEGER NOT NULL DEFAULT 0,
    failure_class  TEXT,
    error          TEXT,
    not_before     TEXT,
    bytes          INTEGER,
    upload_date    TEXT,
    requested_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    done_at        TEXT,
    PRIMARY KEY (library_id, youtube_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS youtube_downloads_wanted_idx
    ON youtube_downloads (library_id, backfill, upload_date)
    WHERE state = 'wanted';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS youtube_downloads_source_idx
    ON youtube_downloads (source_id) WHERE source_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS full_hash TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS superseded_by TEXT
    REFERENCES videos(id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS videos_superseded_by_idx
    ON videos (superseded_by) WHERE superseded_by IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS videos_full_hash_idx
    ON videos (library_id, full_hash) WHERE full_hash IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO worker_pools (pool, label, scope, size, default_size)
VALUES ('convert', 'Convert to MP4', 'global', 1, 1)
ON CONFLICT (pool) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled, disabled_reason)
VALUES
    ('convert_mp4', 'Convert to MP4', '["video"]', 'convert', 'media', 14, 1, 0, 0, 2, '[]', '[]', '[]', 0, 0, 1, 'auto', 'home', 0, 'not_configured'),
    ('dedup_scan', 'Find duplicates', '["library"]', 'nas_read', 'media', 15, 1, 0, 0, 3, '[]', '[]', '[]', 0, 0, 0, 'auto', 'any', 0, 'not_configured'),
    ('dedup_apply', 'Merge duplicates', '["dup_group"]', 'nas_write', 'media', 16, 1, 0, 0, 3, '[]', '[]', '[]', 0, 0, 1, 'auto', 'any', 0, 'not_configured'),
    ('trash_purge', 'Empty file trash', '["library"]', 'nas_write', 'media', 17, 1, 0, 0, 3, '[]', '[]', '[]', 0, 0, 1, 'auto', 'any', 1, NULL),
    ('youtube_fetch', 'YouTube downloads', '["library"]', 'network_yt', 'sources', 2, 1, 0, 0, 3, '[]', '[]', '[]', 0, 0, 1, 'auto', 'any', 0, 'not_configured'),
    ('youtube_backfill', 'YouTube back catalogue', '["library"]', 'network_yt', 'sources', 3, 1, 0, 0, 3, '[]', '[]', '[]', 0, 0, 1, 'auto', 'any', 0, 'not_configured')
ON CONFLICT (stage) DO NOTHING;
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
    rerun_force_cascade      INTEGER NOT NULL DEFAULT 0,
    failure_class            TEXT,
    parked_on                TEXT,

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
    CHECK (subject_kind IN ('video', 'library', 'series', 'season', 'catalog', 'dup_group')),
    CHECK (
        subject_id IS NULL OR
        (subject_kind = 'video'   AND video_id   = subject_id)
     OR (subject_kind = 'library' AND library_id = subject_id AND video_id IS NULL)
     OR (subject_kind = 'series'  AND series_id  = subject_id AND video_id IS NULL)
     OR (subject_kind = 'season'  AND season_id  = subject_id AND video_id IS NULL)
     OR (subject_kind = 'catalog'
         AND subject_id = '00000000-0000-0000-0000-00000000ca7a' AND video_id IS NULL)
     OR (subject_kind = 'dup_group'
         AND video_id IS NULL AND series_id IS NULL AND season_id IS NULL)
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
    created_at, finished_at, subject_kind, subject_id, series_id, season_id,
    stage_version, input_hash, output_hash, outcome, output_summary, force,
    force_cascade, reasons, enqueue_count, triggered_by, triggered_by_stage,
    rerun_requested, rerun_force, rerun_reasons, propagated_at, error_cause,
    progress, started_at, updated_at, rerun_force_cascade, failure_class, parked_on
)
SELECT
    id, video_id, library_id, stage, state, priority, attempts, max_attempts,
    claimed_by, claimed_at, last_heartbeat_at, not_before, error,
    total_duration_seconds, processed_seconds, segments_completed,
    last_segment_end_sec, estimated_remaining_sec, realtime_factor,
    progress_updated_at, pause_requested, cancel_requested, paused_at,
    paused_at_sec, paused_reason, resumed_at, resume_count, metrics, payload,
    created_at, finished_at, subject_kind, subject_id, series_id, season_id,
    stage_version, input_hash, output_hash, outcome, output_summary, force,
    force_cascade, reasons, enqueue_count, triggered_by, triggered_by_stage,
    rerun_requested, rerun_force, rerun_reasons, propagated_at, error_cause,
    progress, started_at, updated_at, rerun_force_cascade, failure_class, parked_on
FROM processing_jobs;
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
CREATE INDEX IF NOT EXISTS processing_jobs_parked_idx
    ON processing_jobs (parked_on, stage) WHERE parked_on IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
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
INSERT INTO youtube_sources (library_id, kind, url, external_id, mode, added_at, status, error)
SELECT yc.library_id, 'channel', yc.url, yc.channel_id, 'off', (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), yc.status, yc.error
  FROM youtube_channels yc
 WHERE yc.library_id IS NOT NULL
ON CONFLICT (library_id, kind, external_id) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM processing_jobs WHERE subject_kind = 'dup_group' OR stage IN ('convert_mp4', 'dedup_scan', 'dedup_apply', 'trash_purge', 'youtube_fetch', 'youtube_backfill');
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
    rerun_force_cascade      INTEGER NOT NULL DEFAULT 0,
    failure_class            TEXT,
    parked_on                TEXT,

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
    created_at, finished_at, subject_kind, subject_id, series_id, season_id,
    stage_version, input_hash, output_hash, outcome, output_summary, force,
    force_cascade, reasons, enqueue_count, triggered_by, triggered_by_stage,
    rerun_requested, rerun_force, rerun_reasons, propagated_at, error_cause,
    progress, started_at, updated_at, rerun_force_cascade, failure_class, parked_on
)
SELECT
    id, video_id, library_id, stage, state, priority, attempts, max_attempts,
    claimed_by, claimed_at, last_heartbeat_at, not_before, error,
    total_duration_seconds, processed_seconds, segments_completed,
    last_segment_end_sec, estimated_remaining_sec, realtime_factor,
    progress_updated_at, pause_requested, cancel_requested, paused_at,
    paused_at_sec, paused_reason, resumed_at, resume_count, metrics, payload,
    created_at, finished_at, subject_kind, subject_id, series_id, season_id,
    stage_version, input_hash, output_hash, outcome, output_summary, force,
    force_cascade, reasons, enqueue_count, triggered_by, triggered_by_stage,
    rerun_requested, rerun_force, rerun_reasons, propagated_at, error_cause,
    progress, started_at, updated_at, rerun_force_cascade, failure_class, parked_on
FROM processing_jobs;
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
CREATE INDEX IF NOT EXISTS processing_jobs_parked_idx
    ON processing_jobs (parked_on, stage) WHERE parked_on IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
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
DELETE FROM job_stages WHERE stage IN ('convert_mp4', 'dedup_scan', 'dedup_apply', 'trash_purge', 'youtube_fetch', 'youtube_backfill');
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM worker_pools WHERE pool = 'convert';
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS videos_full_hash_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS videos_superseded_by_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN superseded_by;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN full_hash;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_downloads;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_sources;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS duplicate_members;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS duplicate_groups;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS video_copies;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS file_actions;
-- +goose StatementEnd
