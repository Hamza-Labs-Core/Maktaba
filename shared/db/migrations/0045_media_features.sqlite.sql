-- +goose Up
-- +goose StatementBegin
--
-- Slot 0045 (Epic 9 / Story 9.10) — content-type classifier features.
--
-- One row per video. `features` is a JSON object with keys
-- `silence_pct`, `music_speech_ratio`, `mean_loudness_lufs`,
-- `diarization_turn_density`, `segment_density`. Populated by the probe
-- + audio-extract stages; read by the categorize stage.
--
CREATE TABLE IF NOT EXISTS media_features (
    video_id    TEXT     PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    features    TEXT     NOT NULL DEFAULT '{}',
    model       TEXT     NOT NULL,
    updated_at  TEXT     NOT NULL DEFAULT (datetime('now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS content_type TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS videos_content_type_idx
    ON videos (content_type)
    WHERE content_type IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_content_type_idx;
ALTER TABLE videos DROP COLUMN IF EXISTS content_type;
DROP TABLE IF EXISTS media_features;
-- +goose StatementEnd
