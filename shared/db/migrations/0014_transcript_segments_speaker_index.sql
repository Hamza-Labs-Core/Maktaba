-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
-- Slot 0014 (Story 3.9 / plan-03-09) — index for diarized lookups.
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_segments_speaker_idx
    ON transcript_segments (transcript_id, speaker)
    WHERE speaker IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS transcript_segments_speaker_idx;
-- +goose StatementEnd
