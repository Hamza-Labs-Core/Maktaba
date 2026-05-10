-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0012 (Story 3.5 / plan-03-05) — transcripts schema.
--
-- Creates the base `transcripts`, `transcript_segments`, and
-- `transcript_words` tables (architecture §8.1) with the partial-unique
-- `is_active` constraint baked in from the start (REVIEW §1.1.b /
-- §1.1.i). Splitting into "create then alter" would land the same end
-- state across two slots; doing it in one keeps the migration history
-- semantic.
--
CREATE TABLE IF NOT EXISTS transcripts (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id            UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    audio_track_id      BIGINT       NOT NULL REFERENCES audio_tracks(id),
    language            TEXT         NOT NULL,
    detected_language   TEXT,
    language_confidence REAL,
    backend             TEXT         NOT NULL,
    model               TEXT         NOT NULL,
    backend_version     TEXT,
    word_level          BOOLEAN      NOT NULL DEFAULT false,
    diarized            BOOLEAN      NOT NULL DEFAULT false,
    quality_score       REAL,
    is_active           BOOLEAN      NOT NULL DEFAULT true,
    metadata            JSONB        NOT NULL DEFAULT '{}'::jsonb,
    superseded_at       TIMESTAMPTZ,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS transcripts_active_unique
    ON transcripts (video_id, audio_track_id)
    WHERE is_active = true;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcripts_video_active_idx
    ON transcripts (video_id) WHERE is_active = true;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS transcript_segments (
    id            BIGSERIAL    PRIMARY KEY,
    transcript_id UUID         NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    seq           INT          NOT NULL,
    start_sec     REAL         NOT NULL,
    end_sec       REAL         NOT NULL,
    text          TEXT         NOT NULL,
    speaker       TEXT,
    confidence    REAL,
    metadata      JSONB        NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (transcript_id, seq)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_segments_time_idx
    ON transcript_segments (transcript_id, start_sec);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS transcript_words (
    id          BIGSERIAL PRIMARY KEY,
    segment_id  BIGINT    NOT NULL REFERENCES transcript_segments(id) ON DELETE CASCADE,
    seq         INT       NOT NULL,
    start_sec   REAL      NOT NULL,
    end_sec     REAL      NOT NULL,
    text        TEXT      NOT NULL,
    confidence  REAL,
    UNIQUE (segment_id, seq)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS transcript_words;
DROP TABLE IF EXISTS transcript_segments;
DROP TABLE IF EXISTS transcripts;
-- +goose StatementEnd
