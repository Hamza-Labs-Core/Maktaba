-- +goose Up
-- +goose StatementBegin
--
-- Slot 0013 (Story 3.6 / plan-03-06) — `commit_segment` function and
-- AFTER INSERT NOTIFY trigger.
--
-- The function ATOMICALLY:
--   1. Inserts a row into `transcript_segments` (UPSERT on
--      `(transcript_id, seq)` is intentional — the orchestrator's
--      retry path may re-emit the same `seq` after a transient
--      failure, and the second insert must be a no-op).
--   2. Updates `processing_jobs` with the new progress markers
--      (last_segment_end_sec, processed_seconds, segments_completed,
--      realtime_factor EWMA, ETA, progress_updated_at,
--      last_heartbeat_at).
--
-- The trigger fires `pg_notify('segments.committed', …)` so the live
-- indexer (Story 5.5) can advance without polling.
--
CREATE OR REPLACE FUNCTION commit_segment(
    p_transcript_id    UUID,
    p_job_id           BIGINT,
    p_seq              INT,
    p_start_sec        REAL,
    p_end_sec          REAL,
    p_text             TEXT,
    p_speaker          TEXT,
    p_confidence       REAL,
    p_audio_sec_in_seg REAL,
    p_wall_sec_in_seg  REAL,
    p_total_duration   REAL,
    p_ewma_alpha       REAL DEFAULT 0.2
) RETURNS BIGINT
LANGUAGE plpgsql AS $$
DECLARE
    v_segment_id   BIGINT;
    v_prev_end     REAL;
    v_prev_factor  REAL;
    v_factor       REAL;
    v_processed    REAL;
    v_eta          REAL;
BEGIN
    -- Snapshot the current job-progress fields. The hot path takes a
    -- single row lock on processing_jobs.id which is O(1).
    SELECT last_segment_end_sec, COALESCE(realtime_factor, 0)
      INTO v_prev_end, v_prev_factor
      FROM processing_jobs
     WHERE id = p_job_id
     FOR UPDATE;

    INSERT INTO transcript_segments
           (transcript_id, seq, start_sec, end_sec, text, speaker, confidence)
    VALUES (p_transcript_id, p_seq, p_start_sec, p_end_sec, p_text, p_speaker, p_confidence)
    ON CONFLICT (transcript_id, seq) DO NOTHING
    RETURNING id INTO v_segment_id;

    -- ON CONFLICT path: a previous attempt already committed this seq.
    -- Treat as no-op — neither progress nor heartbeat advance.
    IF v_segment_id IS NULL THEN
        RETURN NULL;
    END IF;

    -- Audio-time delta (NOT wall-clock). Out-of-order or overlapping
    -- segments still advance progress monotonically.
    v_processed := GREATEST(0, p_end_sec - GREATEST(v_prev_end, p_start_sec));

    -- EWMA over realtime factor (audio_sec / wall_sec). Guard against
    -- zero wall (synthetic backends in tests).
    IF p_wall_sec_in_seg <= 0 THEN
        v_factor := v_prev_factor;
    ELSE
        v_factor := v_prev_factor * (1 - p_ewma_alpha)
                  + (p_audio_sec_in_seg / p_wall_sec_in_seg) * p_ewma_alpha;
    END IF;

    IF v_factor > 0 AND p_total_duration > 0 THEN
        v_eta := (p_total_duration - LEAST(p_end_sec, p_total_duration)) / v_factor;
    ELSE
        v_eta := NULL;
    END IF;

    UPDATE processing_jobs
       SET last_segment_end_sec     = GREATEST(last_segment_end_sec, p_end_sec),
           processed_seconds        = processed_seconds + v_processed,
           segments_completed       = segments_completed + 1,
           realtime_factor          = v_factor,
           estimated_remaining_sec  = v_eta,
           progress_updated_at      = now(),
           last_heartbeat_at        = now()
     WHERE id = p_job_id;

    RETURN v_segment_id;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
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
CREATE OR REPLACE TRIGGER transcript_segments_notify_trg
    AFTER INSERT ON transcript_segments
    FOR EACH ROW
    EXECUTE FUNCTION transcript_segments_notify_fn();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS transcript_segments_notify_trg ON transcript_segments;
DROP FUNCTION IF EXISTS transcript_segments_notify_fn();
DROP FUNCTION IF EXISTS commit_segment(
    UUID, BIGINT, INT, REAL, REAL, TEXT, TEXT, REAL, REAL, REAL, REAL, REAL
);
-- +goose StatementEnd
