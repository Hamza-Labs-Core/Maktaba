-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0186 (Epic 52): transcript_segments.word_data (each
-- sentence's words packed), language_packs (what the packs' registry loaded, a report)
-- and word_flags (suspect words for a home admin to review). reasons, scores and
-- candidates are JSON text.
--
ALTER TABLE transcript_segments ADD COLUMN IF NOT EXISTS word_data BLOB;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS language_packs (
    code        TEXT PRIMARY KEY,
    component   TEXT,
    version     TEXT NOT NULL DEFAULT '',
    data_sha256 TEXT,
    tokenizer   TEXT,
    state       TEXT NOT NULL CHECK (state IN ('loaded', 'off', 'missing', 'error')),
    error       TEXT,
    loaded_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS word_flags (
    id             TEXT    PRIMARY KEY,
    kind           TEXT    NOT NULL DEFAULT 'word' CHECK (kind IN ('word', 'transcript')),
    transcript_id  TEXT    NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    video_id       TEXT    NOT NULL,
    library_id     TEXT    NOT NULL,
    audio_track_id INTEGER,
    segment_seq    INTEGER NOT NULL,
    word_index     INTEGER NOT NULL DEFAULT 0,
    word_count     INTEGER NOT NULL DEFAULT 1,
    char_start     INTEGER NOT NULL,
    char_len       INTEGER NOT NULL,
    start_sec      REAL,
    end_sec        REAL,
    original       TEXT    NOT NULL DEFAULT '',
    context        TEXT    NOT NULL DEFAULT '',
    language       TEXT    NOT NULL DEFAULT '',
    reasons        TEXT    NOT NULL DEFAULT '[]',
    scores         TEXT    NOT NULL DEFAULT '{}',
    priority       REAL    NOT NULL DEFAULT 0,
    candidates     TEXT    NOT NULL DEFAULT '[]',
    key            TEXT    NOT NULL DEFAULT '',
    status         TEXT    NOT NULL DEFAULT 'open'
                   CHECK (status IN ('open', 'accepted', 'corrected', 'name', 'dictionary',
                                     'skipped', 'stale')),
    resolution     TEXT,
    resolved_by    TEXT,
    resolved_at    TEXT,
    locked_by      TEXT,
    locked_until   TEXT,
    pack           TEXT    NOT NULL DEFAULT '',
    check_version  INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (transcript_id, segment_seq, char_start)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS word_flags_queue_idx ON word_flags (library_id, status, priority DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS word_flags_key_idx ON word_flags (key, language, status);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS word_flags_transcript_idx ON word_flags (transcript_id);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled, disabled_reason)
VALUES
    ('word_check', 'Word review', '["video","library"]', 'cpu', 'speech', 4, 1, 0, 0, 3, '[]', '[]', '[]', 0, 0, 0, 'auto', 'any', 1, NULL)
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS word_flags;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS language_packs;
-- +goose StatementEnd
