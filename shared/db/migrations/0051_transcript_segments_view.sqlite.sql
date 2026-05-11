-- +goose Up
-- +goose StatementBegin
DROP VIEW IF EXISTS transcript_segments_v;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE VIEW IF NOT EXISTS transcript_segments_v AS
SELECT  s.id            AS segment_id,
        s.transcript_id AS transcript_id,
        t.video_id      AS video_id,
        t.language      AS transcript_language,
        t.backend       AS transcript_backend,
        t.model         AS transcript_model,
        t.is_active     AS transcript_is_active,
        s.seq           AS seq,
        s.start_sec     AS start_sec,
        s.end_sec       AS end_sec,
        s.text          AS text,
        s.speaker       AS speaker,
        s.confidence    AS confidence,
        s.metadata      AS metadata
FROM    transcript_segments s
        JOIN transcripts t ON t.id = s.transcript_id
WHERE   t.is_active = 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS transcript_segments_v;
-- +goose StatementEnd
