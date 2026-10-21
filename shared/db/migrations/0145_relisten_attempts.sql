-- +goose Up
-- +goose StatementBegin
--
-- Slot 0145 (Epic 38, follow-up of story 38.8) - the second listen's tries per video. A
-- worker that runs the second listen queues transcribe again for the transcripts made
-- without it (a relisten job, engine/stages/transcribe.py). One that still lacks it after
-- its job (a file that worker cannot read, a model that does not load) waits longer each
-- time and, after the last try, is left as it is, said once in the job log.
--
--   attempts     relisten jobs queued for the video since its last transcript made
--                with the second listen (or by an ordinary transcribe job)
--   last_job_id  the latest of them (the job log's note goes on it)
--   last_at      when it was queued
--   next_at      the next one is not queued before this
--   stopped_at   no more tries: the cap was reached (set once, with the note)
--
-- The transcribe stage deletes the row when it stores a transcript with the second
-- listen, or one from an ordinary job (a changed file, an operator's run). Workers write
-- it (slot 0103's default privileges). Additive.
--
CREATE TABLE IF NOT EXISTS relisten_attempts (
    video_id     uuid        PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    attempts     integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_job_id  bigint,
    last_at      timestamptz NOT NULL DEFAULT now(),
    next_at      timestamptz NOT NULL DEFAULT now(),
    stopped_at   timestamptz
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS relisten_attempts;
-- +goose StatementEnd
