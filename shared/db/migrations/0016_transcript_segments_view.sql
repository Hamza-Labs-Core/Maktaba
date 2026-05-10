-- +goose Up
-- +goose StatementBegin
--
-- Slot 0016 (Story 4.5 / plan-04-05) — transcript_segments_v view.
--
-- Filters out superseded transcripts so live-VTT consumers only see
-- segments from the active transcript per video. Cue ordering is by
-- `seq` (monotonic per transcript), not `start_sec` (which is not
-- monotonic across paused/resumed transcripts).
--
CREATE OR REPLACE VIEW transcript_segments_v AS
SELECT
    t.video_id,
    t.id            AS transcript_id,
    t.language_code,
    t.state         AS transcript_state,
    s.id            AS segment_id,
    s.seq,
    s.start_sec,
    s.end_sec,
    s.text,
    s.speaker,
    s.confidence,
    s.committed_at
FROM transcripts t
JOIN transcript_segments s ON s.transcript_id = t.id
WHERE t.is_active = TRUE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS transcript_segments_v;
-- +goose StatementEnd
