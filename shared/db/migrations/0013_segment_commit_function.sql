-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0013 (Story 3.6 / plan-03-06) — transcript_segments + commit_segment().
--
-- Phase 5 prerequisite: ships the table + insert helper that Phase 4
-- (subtitles), Phase 5 (search), and the live-VTT view (slot 0016) read.
--
-- Scope:
--   - `transcript_segments` table — one row per finalized cue.
--   - AFTER INSERT trigger publishes `pg_notify('segments.committed', …)`
--     so the live indexer (slot 0025) and live-VTT consumers can react.
--   - `commit_segment(...)` PL/pgSQL helper that callers use to insert a
--     single segment with monotonic seq enforcement.
--
CREATE TABLE IF NOT EXISTS transcript_segments (
    id              BIGSERIAL    PRIMARY KEY,
    transcript_id   BIGINT       NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    seq             INTEGER      NOT NULL CHECK (seq >= 1),
    start_sec       REAL         NOT NULL CHECK (start_sec >= 0),
    end_sec         REAL         NOT NULL,
    text            TEXT         NOT NULL,
    speaker         TEXT,
    confidence      REAL,
    committed_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (transcript_id, seq),
    CHECK (start_sec <= end_sec)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_segments_tid_seq_idx
    ON transcript_segments (transcript_id, seq);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_segments_tid_start_idx
    ON transcript_segments (transcript_id, start_sec);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION segments_committed_notify() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(
        'segments.committed',
        json_build_object(
            'transcript_id', NEW.transcript_id,
            'segment_id',    NEW.id,
            'seq',           NEW.seq,
            'committed_at',  NEW.committed_at
        )::text
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS segments_committed_notify_trg ON transcript_segments;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER segments_committed_notify_trg
    AFTER INSERT ON transcript_segments
    FOR EACH ROW
    EXECUTE FUNCTION segments_committed_notify();
-- +goose StatementEnd

-- +goose StatementBegin
-- commit_segment(transcript_id, seq, start, end, text, speaker, confidence) → segment_id.
CREATE OR REPLACE FUNCTION commit_segment(
    p_transcript_id BIGINT,
    p_seq           INTEGER,
    p_start_sec     REAL,
    p_end_sec       REAL,
    p_text          TEXT,
    p_speaker       TEXT,
    p_confidence    REAL
) RETURNS BIGINT
LANGUAGE plpgsql AS $$
DECLARE
    v_id BIGINT;
BEGIN
    INSERT INTO transcript_segments
        (transcript_id, seq, start_sec, end_sec, text, speaker, confidence)
    VALUES
        (p_transcript_id, p_seq, p_start_sec, p_end_sec, p_text, p_speaker, p_confidence)
    RETURNING id INTO v_id;
    RETURN v_id;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS commit_segment(BIGINT, INTEGER, REAL, REAL, TEXT, TEXT, REAL);
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS segments_committed_notify_trg ON transcript_segments;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS segments_committed_notify();
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_segments_tid_start_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_segments_tid_seq_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS transcript_segments;
-- +goose StatementEnd
