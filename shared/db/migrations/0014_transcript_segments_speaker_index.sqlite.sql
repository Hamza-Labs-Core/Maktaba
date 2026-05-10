-- +goose Up
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcript_segments_speaker_idx
    ON transcript_segments (transcript_id, speaker)
    WHERE speaker IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_segments_speaker_idx;
-- +goose StatementEnd
