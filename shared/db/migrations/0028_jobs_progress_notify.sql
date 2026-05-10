-- +goose Up
-- +goose StatementBegin
--
-- Slot 0008 (Story 6.3) — `jobs.progress` and `jobs.heartbeat` notify
-- triggers for ``processing_jobs``.
--
-- Two distinct notify channels (plural per the README naming standard):
--   * `jobs.progress` — fired when ``progress_updated_at`` advances.
--     Carries the architecture §7.10 payload byte-for-byte.
--   * `jobs.heartbeat` — fired when ONLY ``last_heartbeat_at`` advances
--     without a progress bump. Consumed by the reaper (Story 6.6); the
--     UI never subscribes (rendering on every 5 s tick would be wasted
--     bandwidth).
--
-- The ELSIF branching ensures a single UPDATE that bumps both columns
-- fires exactly one notify on `jobs.progress`, never also a redundant
-- `jobs.heartbeat`.
--
CREATE OR REPLACE FUNCTION processing_jobs_notify_progress() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.progress_updated_at IS DISTINCT FROM OLD.progress_updated_at THEN
        PERFORM pg_notify(
            'jobs.progress',
            json_build_object(
                'id',                       NEW.id,
                'video_id',                 NEW.video_id,
                'stage',                    NEW.stage,
                'state',                    NEW.state,
                'last_segment_end_sec',     NEW.last_segment_end_sec,
                'processed_seconds',        NEW.processed_seconds,
                'total_duration_seconds',   NEW.total_duration_seconds,
                'segments_completed',       NEW.segments_completed,
                'realtime_factor',          NEW.realtime_factor,
                'estimated_remaining_sec',  NEW.estimated_remaining_sec,
                'updated_at',               NEW.progress_updated_at
            )::text
        );
    ELSIF NEW.last_heartbeat_at IS DISTINCT FROM OLD.last_heartbeat_at THEN
        PERFORM pg_notify(
            'jobs.heartbeat',
            json_build_object(
                'id',                NEW.id,
                'stage',             NEW.stage,
                'last_heartbeat_at', NEW.last_heartbeat_at
            )::text
        );
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER processing_jobs_notify_progress_trg
    AFTER UPDATE OF progress_updated_at, last_heartbeat_at
        ON processing_jobs
    FOR EACH ROW
    EXECUTE FUNCTION processing_jobs_notify_progress();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS processing_jobs_notify_progress_trg ON processing_jobs;
-- +goose StatementEnd
-- +goose StatementBegin
DROP FUNCTION IF EXISTS processing_jobs_notify_progress();
-- +goose StatementEnd
