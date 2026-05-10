-- +goose Up
-- +goose StatementBegin
DROP VIEW IF EXISTS transcript_segments_v;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE VIEW transcript_segments_v AS
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
WHERE t.is_active = 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS transcript_segments_v;
-- +goose StatementEnd
