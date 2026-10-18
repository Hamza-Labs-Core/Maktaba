-- +goose Up
-- +goose StatementBegin
--
-- Slot 0101 (plan 03-10) - the Transcription lane. transcribe, subtitle_gen and index
-- are declared in their own lane, 'speech', rather than the UI guessing it from
-- "media and not legacy" (a media stage moved onto the engine later would have landed
-- under Transcription). Workers re-sync job_stages from the graph at start; this keeps
-- a fresh database consistent first.
--
ALTER TABLE job_stages DROP CONSTRAINT IF EXISTS job_stages_lane_check;
ALTER TABLE job_stages ADD CONSTRAINT job_stages_lane_check
    CHECK (lane IN ('sources','episodes','speech','seasons','channels','media'));
UPDATE job_stages SET lane = 'speech' WHERE stage IN ('transcribe','subtitle_gen','index');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET lane = 'media' WHERE lane = 'speech';
ALTER TABLE job_stages DROP CONSTRAINT IF EXISTS job_stages_lane_check;
ALTER TABLE job_stages ADD CONSTRAINT job_stages_lane_check
    CHECK (lane IN ('sources','episodes','seasons','channels','media'));
-- +goose StatementEnd
