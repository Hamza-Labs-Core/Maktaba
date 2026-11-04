-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0175 - the queue reads stop scanning the jobs' history.
--
-- On the home (3.3M processing_jobs rows, 2.8 GB) GET /api/queues/libraries took 18-34 s
-- and often failed, GET /api/queues 18-20 s, and the cluster hub's queue snapshot timed
-- out every 10 s: the library strip counted DISTINCT done subjects over every done row,
-- and the snapshot's counts read the done rows with a sequential scan.
--
-- 1. processing_done_subjects: one row per (library, stage, subject) that has a done job,
--    with the latest time one of them finished (last_at). Triggers on processing_jobs keep
--    it: a row inserted done, or turned done, adds its subject (or moves last_at on); a
--    row that leaves done or its library, and a DELETE (the retention, a removed video or
--    library), take the subject away once no done row is left for it. GET
--    /api/queues/libraries counts these rows instead of the jobs: one row per subject
--    however many times it ran (fp_gate: 1.1M done rows, 2.8k subjects). The triggers
--    only touch the subject's own row, so two jobs finishing never wait on each other.
--    The table is backfilled here from the done rows.
-- 2. processing_jobs_live_idx: the live rows (pending .. paused) by stage and state,
--    with every column the snapshot's live counts and the library strip read, so both
--    are index-only scans of the backlog (168k rows on the home), never of the history.
-- 3. processing_jobs_done_finished_idx: the done rows by stage and finish time, with their
--    outcome: the 24 h, health-window and wave counts read one range per stage.
-- 4. processing_jobs_awaiting_review_idx: the done rows waiting for review (a handful;
--    the snapshot's awaiting-review count was a 12 s sequential scan).
--
-- Additive: one table, four trigger functions, four triggers, three indexes.
--
CREATE TABLE IF NOT EXISTS processing_done_subjects (
    library_id   UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    stage        TEXT        NOT NULL,
    subject_id   UUID        NOT NULL,
    subject_kind TEXT        NOT NULL,
    last_at      TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (library_id, stage, subject_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
COMMENT ON TABLE processing_done_subjects IS
    'Slot 0175: the subjects with a done job per library and stage (GET /api/queues/libraries), kept by the processing_jobs triggers';
-- +goose StatementEnd

-- +goose StatementBegin
-- a row inserted done, turned done, moved to a library or finished again
CREATE OR REPLACE FUNCTION processing_done_subjects_add() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO processing_done_subjects AS d (library_id, stage, subject_id, subject_kind, last_at)
    VALUES (NEW.library_id, NEW.stage, NEW.subject_id, NEW.subject_kind,
            COALESCE(NEW.finished_at, NEW.created_at))
    ON CONFLICT (library_id, stage, subject_id) DO UPDATE
        SET last_at = EXCLUDED.last_at
      WHERE d.last_at < EXCLUDED.last_at;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- a done row that left done or its library: its subject goes when no done row is left
CREATE OR REPLACE FUNCTION processing_done_subjects_left() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM processing_done_subjects d
     WHERE d.library_id = OLD.library_id AND d.stage = OLD.stage AND d.subject_id = OLD.subject_id
       AND NOT EXISTS (SELECT 1 FROM processing_jobs p
                        WHERE p.subject_kind = OLD.subject_kind AND p.subject_id = OLD.subject_id
                          AND p.stage = OLD.stage AND p.state = 'done'
                          AND p.library_id = OLD.library_id);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- deleted rows (one statement: the retention's batches, a removed video's cascade)
CREATE OR REPLACE FUNCTION processing_done_subjects_gone() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM processing_done_subjects d
     USING (SELECT DISTINCT library_id, stage, subject_id, subject_kind
              FROM gone WHERE state = 'done' AND library_id IS NOT NULL) g
     WHERE d.library_id = g.library_id AND d.stage = g.stage AND d.subject_id = g.subject_id
       AND NOT EXISTS (SELECT 1 FROM processing_jobs p
                        WHERE p.subject_kind = g.subject_kind AND p.subject_id = g.subject_id
                          AND p.stage = g.stage AND p.state = 'done'
                          AND p.library_id = g.library_id);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER processing_done_subjects_ins_trg
    AFTER INSERT ON processing_jobs
    FOR EACH ROW
    WHEN (NEW.state = 'done' AND NEW.library_id IS NOT NULL)
    EXECUTE FUNCTION processing_done_subjects_add();
-- +goose StatementEnd

-- +goose StatementBegin
-- a heartbeat or a progress write names none of these columns: the trigger does not fire
CREATE OR REPLACE TRIGGER processing_done_subjects_upd_trg
    AFTER UPDATE OF state, library_id, finished_at ON processing_jobs
    FOR EACH ROW
    WHEN (NEW.state = 'done' AND NEW.library_id IS NOT NULL
          AND (OLD.state IS DISTINCT FROM 'done'
               OR OLD.library_id IS DISTINCT FROM NEW.library_id
               OR OLD.finished_at IS DISTINCT FROM NEW.finished_at))
    EXECUTE FUNCTION processing_done_subjects_add();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER processing_done_subjects_left_trg
    AFTER UPDATE OF state, library_id ON processing_jobs
    FOR EACH ROW
    WHEN (OLD.state = 'done' AND OLD.library_id IS NOT NULL
          AND (NEW.state IS DISTINCT FROM 'done' OR NEW.library_id IS DISTINCT FROM OLD.library_id))
    EXECUTE FUNCTION processing_done_subjects_left();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER processing_done_subjects_gone_trg
    AFTER DELETE ON processing_jobs
    REFERENCING OLD TABLE AS gone
    FOR EACH STATEMENT
    EXECUTE FUNCTION processing_done_subjects_gone();
-- +goose StatementEnd

-- +goose StatementBegin
-- after the triggers: a row turned done meanwhile is in the table either way
INSERT INTO processing_done_subjects AS d (library_id, stage, subject_id, subject_kind, last_at)
SELECT library_id, stage, subject_id, min(subject_kind), max(COALESCE(finished_at, created_at))
  FROM processing_jobs
 WHERE state = 'done' AND library_id IS NOT NULL
 GROUP BY library_id, stage, subject_id
ON CONFLICT (library_id, stage, subject_id) DO UPDATE
    SET last_at = GREATEST(d.last_at, EXCLUDED.last_at);
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- the workers finish jobs and the retention deletes them: the triggers write here
        -- as they do (the group's default privileges give the same)
        GRANT SELECT, INSERT, UPDATE, DELETE ON processing_done_subjects TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_live_idx
    ON processing_jobs (stage, state)
    INCLUDE (library_id, not_before, attempts, created_at, error_cause)
    WHERE state IN ('pending', 'claimed', 'running', 'resuming', 'paused');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_done_finished_idx
    ON processing_jobs (stage, finished_at)
    INCLUDE (outcome)
    WHERE state = 'done';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_awaiting_review_idx
    ON processing_jobs (stage)
    WHERE state = 'done' AND outcome = 'awaiting_review';
-- +goose StatementEnd

-- +goose StatementBegin
ANALYZE processing_jobs;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- dev only (README rule 6)
DROP INDEX IF EXISTS processing_jobs_awaiting_review_idx;
DROP INDEX IF EXISTS processing_jobs_done_finished_idx;
DROP INDEX IF EXISTS processing_jobs_live_idx;
DROP TRIGGER IF EXISTS processing_done_subjects_gone_trg ON processing_jobs;
DROP TRIGGER IF EXISTS processing_done_subjects_left_trg ON processing_jobs;
DROP TRIGGER IF EXISTS processing_done_subjects_upd_trg ON processing_jobs;
DROP TRIGGER IF EXISTS processing_done_subjects_ins_trg ON processing_jobs;
DROP FUNCTION IF EXISTS processing_done_subjects_gone();
DROP FUNCTION IF EXISTS processing_done_subjects_left();
DROP FUNCTION IF EXISTS processing_done_subjects_add();
DROP TABLE IF EXISTS processing_done_subjects;
-- +goose StatementEnd
