-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0045 (Epic 9 / Story 9.10) — content-type classifier features.
--
-- `features` is a JSONB object with documented keys (architecture §8.1):
-- `silence_pct`, `music_speech_ratio`, `mean_loudness_lufs`,
-- `diarization_turn_density`, `segment_density`. Populated by the probe
-- + audio-extract stages; read by the categorize stage.
--
CREATE TABLE IF NOT EXISTS media_features (
    video_id    UUID         PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    features    JSONB        NOT NULL DEFAULT '{}'::jsonb,
    model       TEXT         NOT NULL,
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS content_type TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_content_type_idx
    ON videos (content_type)
    WHERE content_type IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_content_type_idx;
ALTER TABLE videos DROP COLUMN IF EXISTS content_type;
DROP TABLE IF EXISTS media_features;
-- +goose StatementEnd
