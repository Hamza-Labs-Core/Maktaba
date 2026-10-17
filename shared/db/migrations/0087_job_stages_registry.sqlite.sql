-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0087 (plan 06-11 section 10.1).
--
-- Type swaps follow architecture section 8.0: UUID and TIMESTAMPTZ become
-- TEXT, BOOLEAN becomes INTEGER, JSONB and TEXT[] become TEXT (a JSON
-- document or array). The Postgres file also drops the three stage CHECK
-- constraints, adds the stage foreign key and the updated_at column with its
-- touch trigger; on SQLite all of that is done by the slot 0088 table
-- rebuild, because SQLite cannot alter a CHECK or add a foreign key in place.
--
CREATE TABLE IF NOT EXISTS worker_pools (
    pool          TEXT PRIMARY KEY CHECK (length(pool) BETWEEN 2 AND 32),
    label         TEXT NOT NULL,
    scope         TEXT NOT NULL CHECK (scope IN ('global','per_worker')),
    size          INTEGER CHECK (size IS NULL OR size BETWEEN 0 AND 64),
    default_size  INTEGER,
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_by    TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT OR IGNORE INTO worker_pools (pool, label, scope, size, default_size) VALUES
    ('nas_read', 'NAS reads', 'global', 2, 2),
    ('cpu', 'CPU', 'per_worker', NULL, NULL),
    ('gpu', 'GPU', 'per_worker', 1, 1),
    ('network_plex', 'Plex', 'global', 2, 2),
    ('network_tv', 'dizqueTV', 'global', 1, 1),
    ('network_yt', 'YouTube', 'global', 1, 1)
;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS job_stages (
    stage            TEXT PRIMARY KEY CHECK (length(stage) BETWEEN 2 AND 40),
    label            TEXT NOT NULL,
    subject_kinds    TEXT NOT NULL,
    pool             TEXT NOT NULL REFERENCES worker_pools(pool),
    lane             TEXT NOT NULL CHECK (lane IN ('sources','episodes','seasons','channels','media')),
    lane_order       INTEGER NOT NULL DEFAULT 0,
    version          INTEGER NOT NULL DEFAULT 1,
    debounce_sec     INTEGER NOT NULL DEFAULT 0,
    debounce_max_sec INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER NOT NULL DEFAULT 3,
    edges            TEXT NOT NULL DEFAULT '[]',
    may_request      TEXT NOT NULL DEFAULT '[]',
    entry_for        TEXT NOT NULL DEFAULT '[]',
    legacy           INTEGER NOT NULL DEFAULT 0,
    hidden           INTEGER NOT NULL DEFAULT 0,
    external_write   INTEGER NOT NULL DEFAULT 0,
    extension        TEXT,
    enabled          INTEGER NOT NULL DEFAULT 1,
    disabled_reason  TEXT,
    paused           INTEGER NOT NULL DEFAULT 0,
    paused_at        TEXT,
    paused_by        TEXT,
    write_mode       TEXT NOT NULL DEFAULT 'auto' CHECK (write_mode IN ('auto','review')),
    graph_synced_at  TEXT,
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT OR IGNORE INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode)
VALUES
    ('youtube_sync', 'YouTube listing', '["library"]', 'network_yt', 'sources', 0, 1, 300, 3600, 3, '[{"to":"link","map":"videos_of","when":"output_changed","legacy":false},{"to":"detect","map":"same","when":"output_changed","legacy":false}]', '[]', '["library"]', 0, 0, 0, 'auto'),
    ('probe', 'Length', '["video"]', 'nas_read', 'episodes', 1, 1, 0, 0, 3, '[{"to":"thumb_hash","map":"same","when":"output_changed","legacy":false},{"to":"link","map":"same","when":"output_changed","legacy":false},{"to":"detect","map":"library_of","when":"output_changed","legacy":false},{"to":"extract","map":"same","when":"output_changed","legacy":true}]', '[]', '[]', 1, 0, 0, 'auto'),
    ('thumb_hash', 'Thumbnail', '["video"]', 'nas_read', 'episodes', 2, 1, 0, 0, 3, '[{"to":"detect","map":"library_of","when":"output_changed","legacy":false}]', '[]', '[]', 0, 0, 0, 'auto'),
    ('link', 'Link', '["video"]', 'cpu', 'episodes', 3, 1, 0, 0, 3, '[{"to":"detect","map":"library_of","when":"output_changed","legacy":false}]', '[]', '[]', 0, 0, 0, 'auto'),
    ('fingerprint', 'Fingerprint', '["video"]', 'nas_read', 'episodes', 4, 101, 0, 0, 3, '[{"to":"detect","map":"library_of","when":"output_changed","legacy":false},{"to":"fp_gate","map":"payload_seasons","when":"output_changed","legacy":false}]', '[]', '[]', 0, 0, 0, 'auto'),
    ('style_sample', 'Style sample', '["video"]', 'nas_read', 'episodes', 5, 1, 0, 0, 3, '[{"to":"rate_style","map":"payload_seasons","when":"output_changed","legacy":false}]', '[]', '[]', 0, 0, 0, 'auto'),
    ('detect', 'Place episodes', '["library"]', 'cpu', 'seasons', 1, 2, 30, 300, 3, '[{"to":"tag","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"rate_style","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"tag","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]', '["fingerprint","fp_gate"]', '["library"]', 0, 0, 0, 'auto'),
    ('fp_gate', 'Samples to candidates', '["season"]', 'cpu', 'seasons', 1, 101, 10, 120, 3, '[]', '["fingerprint"]', '[]', 0, 1, 0, 'auto'),
    ('tag', 'Tag topics', '["season","library"]', 'cpu', 'seasons', 2, 1, 0, 0, 3, '[{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]', '[]', '["season"]', 0, 0, 0, 'auto'),
    ('rate_style', 'Rate style', '["season"]', 'cpu', 'seasons', 3, 1, 10, 120, 3, '[{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]', '["style_sample"]', '["season"]', 0, 0, 0, 'auto'),
    ('plan_channels', 'Plan channels', '["catalog"]', 'cpu', 'channels', 1, 1, 60, 900, 3, '[{"to":"write_channels","map":"same","when":"output_changed","legacy":false}]', '[]', '["catalog"]', 0, 0, 0, 'auto'),
    ('write_channels', 'Write channels', '["catalog"]', 'cpu', 'channels', 2, 1, 0, 0, 3, '[]', '[]', '[]', 0, 0, 0, 'auto'),
    ('scan', 'Scan folders', '["library"]', 'nas_read', 'media', 0, 1, 0, 0, 3, '[{"to":"probe","map":"videos_of","when":"output_changed","legacy":true}]', '[]', '[]', 1, 0, 0, 'auto'),
    ('extract', 'Extract audio', '["video"]', 'nas_read', 'media', 1, 1, 0, 0, 3, '[{"to":"transcribe","map":"same","when":"output_changed","legacy":true}]', '[]', '[]', 1, 0, 0, 'auto'),
    ('transcribe', 'Transcribe', '["video"]', 'gpu', 'media', 2, 1, 0, 0, 3, '[{"to":"subtitle_gen","map":"same","when":"output_changed","legacy":true},{"to":"index","map":"same","when":"output_changed","legacy":true}]', '[]', '[]', 1, 0, 0, 'auto'),
    ('subtitle_gen', 'Subtitles', '["video"]', 'cpu', 'media', 3, 1, 0, 0, 3, '[]', '[]', '[]', 1, 0, 0, 'auto'),
    ('index', 'Index', '["video"]', 'cpu', 'media', 4, 1, 0, 0, 3, '[{"to":"thumbnail","map":"same","when":"output_changed","legacy":true}]', '[]', '[]', 1, 0, 0, 'auto'),
    ('thumbnail', 'Poster and sprites', '["video"]', 'nas_read', 'media', 5, 1, 0, 0, 3, '[]', '[]', '[]', 1, 0, 0, 'auto'),
    ('topic_recluster', 'Recluster topics', '["library"]', 'cpu', 'media', 6, 1, 0, 0, 3, '[]', '[]', '[]', 1, 0, 0, 'auto'),
    ('topic_assign', 'Assign topics', '["video"]', 'cpu', 'media', 7, 1, 0, 0, 3, '[]', '[]', '[]', 1, 0, 0, 'auto'),
    ('categorize', 'Categorize', '["video"]', 'cpu', 'media', 8, 1, 0, 0, 3, '[]', '[]', '[]', 1, 0, 0, 'auto'),
    ('chapter_infer', 'Infer chapters', '["video"]', 'cpu', 'media', 9, 1, 0, 0, 3, '[]', '[]', '[]', 1, 0, 0, 'auto')
;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS job_stages;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS worker_pools;
-- +goose StatementEnd
