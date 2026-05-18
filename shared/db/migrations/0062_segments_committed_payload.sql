-- +goose Up
-- +goose StatementBegin
--
-- Slot 0062 (gap-closure Wave 2 / Epic 03 Story 3.6-4, HLB-324) —
-- correct the `segments.committed` NOTIFY payload key.
--
-- Why this exists
-- ----------------
-- Slot 0013's `transcript_segments_notify_fn()` fired
-- `pg_notify('segments.committed', {transcript_id, segment_id, seq,
-- end_sec})`. Story 3.6-4 and the Epic-5.5 live-indexer / live-VTT
-- contract specify the watermark key as `last_segment_end_sec` — the
-- advanced job progress marker, NOT this row's raw `end_sec`. A
-- consumer written to the AC contract keyed off `last_segment_end_sec`
-- and silently never advanced because the trigger only ever emitted
-- `end_sec`. There was no consumer yet (the incremental indexer is not
-- built), so renaming the key now is forward-correct with no migration
-- of in-flight data.
--
-- For the straight-through monotonic commit path the two values are
-- equal (`commit_segment` advances the job watermark to this segment's
-- end). Sourcing the bounded NOTIFY from `NEW.end_sec` under the
-- contract name keeps the trigger a pure AFTER INSERT projection (it
-- has no access to the post-UPDATE `processing_jobs` row and must not
-- take a second lock on the hot path); a non-monotonic / clamped
-- orchestrator is a separate deferred story and would re-notify with
-- the clamped value it writes.
--
-- This mirrors the SQLite in-process-bus payload
-- (`stt/segment_commit.py`) one-for-one so both dialects emit the
-- identical `{transcript_id, segment_id, seq, last_segment_end_sec}`
-- shape.
--
-- Idempotent: CREATE OR REPLACE FUNCTION + DROP TRIGGER IF EXISTS /
-- CREATE OR REPLACE TRIGGER make re-application a no-op. This file uses
-- goose's StatementBegin / StatementEnd markers around each DDL
-- statement.
--
CREATE OR REPLACE FUNCTION transcript_segments_notify_fn()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(
        'segments.committed',
        json_build_object(
            'transcript_id', NEW.transcript_id::text,
            'segment_id', NEW.id,
            'seq', NEW.seq,
            'last_segment_end_sec', NEW.end_sec
        )::text
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS transcript_segments_notify_trg ON transcript_segments;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER transcript_segments_notify_trg
    AFTER INSERT ON transcript_segments
    FOR EACH ROW
    EXECUTE FUNCTION transcript_segments_notify_fn();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
--
-- Restore the slot-0013 payload (the `end_sec` key) so a down-migration
-- lands the exact pre-0062 function body. Down migrations are dev-only.
--
CREATE OR REPLACE FUNCTION transcript_segments_notify_fn()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(
        'segments.committed',
        json_build_object(
            'transcript_id', NEW.transcript_id::text,
            'segment_id', NEW.id,
            'seq', NEW.seq,
            'end_sec', NEW.end_sec
        )::text
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS transcript_segments_notify_trg ON transcript_segments;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER transcript_segments_notify_trg
    AFTER INSERT ON transcript_segments
    FOR EACH ROW
    EXECUTE FUNCTION transcript_segments_notify_fn();
-- +goose StatementEnd
