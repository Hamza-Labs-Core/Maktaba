-- +goose Up
-- +goose StatementBegin
--
-- Slot 0051 (Story 4.5 / plan-04-05) — `transcript_segments_v` convenience view.
--
-- Joins `transcript_segments` with its parent `transcripts` (and through
-- the parent to the `videos.id`) so subtitle-generation and API code can
-- query one shape instead of writing the join on every call site. The
-- view is read-only; writes still go through the base tables.
--
-- Only rows belonging to active transcripts are exposed — superseded
-- transcript rows would surface stale segment timings and confuse
-- downstream consumers. Callers needing inactive segments should query
-- `transcript_segments` directly.
--
CREATE OR REPLACE VIEW transcript_segments_v AS
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
WHERE   t.is_active = true;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS transcript_segments_v;
-- +goose StatementEnd
