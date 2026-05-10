-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0009 (Story 2.1 / plan-02-02) — audio probe schema.
--
-- Creates the two probe-output tables (`media_info`, `audio_tracks`)
-- and adds the columns that track-selection (Story 2.2) reads:
--
--   - `audio_tracks.disposition`            ffprobe disposition object
--   - `audio_tracks.detected_language`      ISO 639-3, populated by STT
--   - `audio_tracks.detected_language_confidence`  0..1, set by language-tag stage
--
-- The base shape comes from architecture.md §8.1; the extension columns
-- come from plan-02-02 §3.3. They land in a single slot because the
-- track-selection function in Python reads both shapes — splitting them
-- across two slots offers no operational benefit.
--
-- Why NO TRANSACTION: `CREATE INDEX CONCURRENTLY` cannot run inside a
-- transaction; each statement below is individually idempotent so a
-- half-applied migration is safe to re-run.
--
CREATE TABLE IF NOT EXISTS media_info (
    video_id        UUID         PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    container       TEXT,
    video_codec     TEXT,
    width           INT,
    height          INT,
    fps             REAL,
    bitrate_kbps    INT,
    has_subtitles   BOOLEAN      NOT NULL DEFAULT false,
    raw_ffprobe     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    probed_at       TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS audio_tracks (
    id            BIGSERIAL    PRIMARY KEY,
    video_id      UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    track_index   INT          NOT NULL,
    codec         TEXT,
    channels      INT,
    sample_rate   INT,
    language      TEXT         NOT NULL DEFAULT 'und',
    title         TEXT,
    is_default    BOOLEAN      NOT NULL DEFAULT false,
    disposition   JSONB        NOT NULL DEFAULT '{}'::jsonb,
    detected_language            TEXT,
    detected_language_confidence REAL CHECK (detected_language_confidence IS NULL
                                              OR (detected_language_confidence >= 0
                                                  AND detected_language_confidence <= 1)),
    UNIQUE (video_id, track_index)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS audio_tracks_video_idx
    ON audio_tracks (video_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audio_tracks;
DROP TABLE IF EXISTS media_info;
-- +goose StatementEnd
