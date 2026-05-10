-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0035 (Epic 7 / Story 7.14) — speaker registry + segment links.
--
-- Diarisation produces opaque ``cluster_label`` strings; the API lets a
-- user rename them and merge clusters. Names are unique per video to
-- support per-video aliasing (the same person may be "Sheikh A" in one
-- lecture series and "Imam A" in another).
--
CREATE TABLE IF NOT EXISTS speakers (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id      UUID         REFERENCES videos(id) ON DELETE CASCADE,
    cluster_label TEXT,
    name          TEXT         NOT NULL,
    metadata      JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (video_id, name)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS segment_speakers (
    segment_id BIGINT NOT NULL REFERENCES transcript_segments(id) ON DELETE CASCADE,
    speaker_id UUID   NOT NULL REFERENCES speakers(id) ON DELETE CASCADE,
    confidence REAL,
    PRIMARY KEY (segment_id, speaker_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS segment_speakers_speaker_idx
    ON segment_speakers (speaker_id, segment_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS segment_speakers_speaker_idx;
DROP TABLE IF EXISTS segment_speakers;
DROP TABLE IF EXISTS speakers;
-- +goose StatementEnd
