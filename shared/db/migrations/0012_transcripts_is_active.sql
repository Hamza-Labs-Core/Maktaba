-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0012 (Story 3.5 / plan-03-05) — transcripts table.
--
-- Phase 5 prerequisite: Phase 3 (transcription) hasn't shipped yet, so
-- this migration creates the canonical `transcripts` table that Phase 4
-- (subtitles) and Phase 5 (search) depend on. When Phase 3 lands its own
-- transcription logic, it can ALTER the existing table rather than
-- recreate it.
--
-- Scope:
--   - `transcripts` row per (video_id, attempt) with a single `is_active`
--     row per video at any time (partial unique index).
--   - `metadata JSONB` for backend-specific knobs (model name, version,
--     speed_preset, etc.) and per-row metrics.
--
-- Out of scope (other slots):
--   - `transcript_segments` and `commit_segment()` → slot 0013.
--   - `transcript_segments(transcript_id, speaker)` index → slot 0014.
--
CREATE TABLE IF NOT EXISTS transcripts (
    id              BIGSERIAL    PRIMARY KEY,
    video_id        UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    language_code   TEXT         NOT NULL,
    backend         TEXT         NOT NULL,
    model           TEXT,
    state           TEXT         NOT NULL DEFAULT 'running'
                                 CHECK (state IN ('running','done','paused','failed','superseded')),
    is_active       BOOLEAN      NOT NULL DEFAULT TRUE,
    last_indexed_segment_seq INTEGER NOT NULL DEFAULT 0,
    metadata        JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcripts_video_idx
    ON transcripts (video_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Partial unique: at most one is_active=true row per video_id.
-- Resolves REVIEW §1.1.b.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS transcripts_video_active_uq
    ON transcripts (video_id) WHERE is_active = TRUE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS transcripts_video_active_uq;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS transcripts_video_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS transcripts;
-- +goose StatementEnd
