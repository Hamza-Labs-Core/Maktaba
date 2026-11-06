-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0186 - word review, part 1 (Epic 52, stories 52.1, 52.3, 52.6, 52.8): the evidence
-- a transcript keeps for each word, what the language packs loaded, and the suspect words
-- a home admin reviews.
--
-- 1. transcript_segments.word_data: each sentence's words packed, 9 bytes a word (time,
--    Whisper's probability, the second listen's marks), pointing into the sentence's text
--    by code point (pipeline stt/word_data.py, api/internal/transcript/worddata). Nullable,
--    no default: metadata-only on the 23 M-row table. Old transcripts have none.
-- 2. language_packs: a report, one row per pack the pipeline's registry loaded on a
--    machine (state loaded | off | missing | error). No switch: the switch is the pack's
--    Epic 51 component (lang.ar, lang.en, lang.hunspell.<code>).
-- 3. word_flags: one row per suspect word or short span (word_check), or one per
--    transcript that may be poor (kind 'transcript', segment_seq -1). The soft lock
--    (locked_by, locked_until) is 52.8's.
--
-- 4. job_stages: the word_check stage.
--
-- Additive: one column, two tables, three indexes, one stage row (CONCURRENTLY: NO TRANSACTION, every
-- statement harmless twice).
--
ALTER TABLE transcript_segments ADD COLUMN IF NOT EXISTS word_data BYTEA;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS language_packs (
    code        TEXT        PRIMARY KEY,
    component   TEXT,
    version     TEXT        NOT NULL DEFAULT '',
    data_sha256 TEXT,
    tokenizer   TEXT,
    state       TEXT        NOT NULL CHECK (state IN ('loaded', 'off', 'missing', 'error')),
    error       TEXT,
    loaded_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS word_flags (
    id             UUID        PRIMARY KEY,
    kind           TEXT        NOT NULL DEFAULT 'word' CHECK (kind IN ('word', 'transcript')),
    transcript_id  UUID        NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    video_id       UUID        NOT NULL,
    library_id     UUID        NOT NULL,
    audio_track_id BIGINT,
    segment_seq    INTEGER     NOT NULL,
    word_index     SMALLINT    NOT NULL DEFAULT 0,
    word_count     SMALLINT    NOT NULL DEFAULT 1,
    char_start     INTEGER     NOT NULL,
    char_len       INTEGER     NOT NULL,
    start_sec      REAL,
    end_sec        REAL,
    original       TEXT        NOT NULL DEFAULT '',
    context        TEXT        NOT NULL DEFAULT '',
    language       TEXT        NOT NULL DEFAULT '',
    reasons        TEXT[]      NOT NULL DEFAULT '{}',
    scores         JSONB       NOT NULL DEFAULT '{}',
    priority       REAL        NOT NULL DEFAULT 0,
    candidates     JSONB       NOT NULL DEFAULT '[]',
    key            TEXT        NOT NULL DEFAULT '',
    status         TEXT        NOT NULL DEFAULT 'open'
                   CHECK (status IN ('open', 'accepted', 'corrected', 'name', 'dictionary',
                                     'skipped', 'stale')),
    resolution     TEXT,
    resolved_by    UUID,
    resolved_at    TIMESTAMPTZ,
    locked_by      UUID,
    locked_until   TIMESTAMPTZ,
    pack           TEXT        NOT NULL DEFAULT '',
    check_version  INTEGER     NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (transcript_id, segment_seq, char_start)
);
-- +goose StatementEnd

-- +goose StatementBegin
COMMENT ON TABLE word_flags IS
    'Slot 0186 (Epic 52): suspect transcript words for a home admin to review, written by word_check';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS word_flags_queue_idx ON word_flags (library_id, status, priority DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS word_flags_key_idx ON word_flags (key, language, status);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS word_flags_transcript_idx ON word_flags (transcript_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- the word_check stage (Epic 52, story 52.6): video and library subjects, the cpu pool,
-- speech lane 4; its edges are the pipeline's to write (its graph sync)
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled, disabled_reason)
VALUES
    ('word_check', 'Word review', ARRAY['video', 'library']::text[], 'cpu', 'speech', 4, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'any', true, NULL)
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- word_check runs on any machine: it writes the flags and its pack report
        GRANT SELECT, INSERT, UPDATE, DELETE ON word_flags TO maktaba_worker;
        GRANT SELECT, INSERT, UPDATE, DELETE ON language_packs TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS word_flags;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS language_packs;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE transcript_segments DROP COLUMN IF EXISTS word_data;
-- +goose StatementEnd
