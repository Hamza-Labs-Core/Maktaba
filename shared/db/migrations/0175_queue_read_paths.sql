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
--    with the latest time one of them finished (last_at). Statement triggers on
--    processing_jobs keep it: a row inserted done, or turned done, adds its subject (or
--    moves last_at on); a row that leaves done or its library, and a DELETE (the
--    retention, a removed video or library), take the subject away once no done row is
--    left for it. Every path locks a subject's row before it decides, and a statement
--    takes its subjects in key order, so two writers never both keep or both drop one
--    and never wait on each other in a circle. GET
--    /api/queues/libraries counts these rows instead of the jobs: one row per subject
--    however many times it ran (fp_gate: 1.1M done rows, 2.8k subjects). A job finishing
--    touches only its own subject's row: jobs of other subjects never wait on it.
--    The table is backfilled here from the done rows.
-- 2. processing_jobs_live_idx: the live rows (pending .. paused) by stage and state,
--    with every column the snapshot's live counts and the library strip read, so both
--    are index-only scans of the backlog (168k rows on the home), never of the history.
-- 3. processing_jobs_done_finished_idx: the done rows by stage and finish time, with their
--    outcome: the 24 h, health-window and wave counts read one range per stage.
-- 4. processing_jobs_awaiting_review_idx: the done rows waiting for review (a handful;
--    the snapshot's awaiting-review count was a 12 s sequential scan).
--
-- Additive: one table, four functions, three triggers, three indexes.
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
-- One subject's summary row, the only place that writes it. add: a done row of the
-- subject is in the library (insert it, or move last_at on). Not add: a done row left;
-- the row is locked, then (a new statement, so a new snapshot that sees whatever the
-- lock waited for) it goes when no done row of the subject is left in the library.
-- Both branches lock the row (ON CONFLICT DO UPDATE locks it even when its WHERE
-- does not hold), so whoever comes second for a subject waits for the first to end.
CREATE OR REPLACE FUNCTION processing_done_subjects_touch(
    p_library UUID, p_stage TEXT, p_subject UUID, p_kind TEXT, p_last TIMESTAMPTZ, p_add BOOLEAN
) RETURNS VOID
LANGUAGE plpgsql AS $$
BEGIN
    IF p_add THEN
        INSERT INTO processing_done_subjects AS d (library_id, stage, subject_id, subject_kind, last_at)
        VALUES (p_library, p_stage, p_subject, p_kind, p_last)
        ON CONFLICT (library_id, stage, subject_id) DO UPDATE
            SET last_at = EXCLUDED.last_at
          WHERE d.last_at < EXCLUDED.last_at;
        RETURN;
    END IF;
    PERFORM 1 FROM processing_done_subjects d
      WHERE d.library_id = p_library AND d.stage = p_stage AND d.subject_id = p_subject
        FOR UPDATE;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    DELETE FROM processing_done_subjects d
     WHERE d.library_id = p_library AND d.stage = p_stage AND d.subject_id = p_subject
       AND NOT EXISTS (SELECT 1 FROM processing_jobs j
                        WHERE j.subject_kind = p_kind AND j.subject_id = p_subject
                          AND j.stage = p_stage AND j.state = 'done'
                          AND j.library_id = p_library);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The three triggers are statement triggers over the statement's rows, and each
-- touches its subjects in the summary's key order (library_id, stage, subject_id):
-- a retention batch and a statement finishing many subjects lock the same rows in
-- the same order, so they never wait on each other in a circle (a row trigger runs
-- in the order the rows came, which two statements do not share). A finished job is
-- one statement in its own transaction (jobs_state.mark_done, engine/propagate.py).
--
-- rows inserted (a bulk import's done rows among them)
CREATE OR REPLACE FUNCTION processing_done_subjects_added() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE r RECORD;
BEGIN
    FOR r IN
        SELECT library_id, stage, subject_id, min(subject_kind) AS kind,
               max(COALESCE(finished_at, created_at)) AS last
          FROM added
         WHERE state = 'done' AND library_id IS NOT NULL AND subject_id IS NOT NULL
         GROUP BY library_id, stage, subject_id
         ORDER BY library_id, stage, subject_id
    LOOP
        PERFORM processing_done_subjects_touch(r.library_id, r.stage, r.subject_id, r.kind, r.last, TRUE);
    END LOOP;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- rows updated: turned done, moved, finished again (add); left done or moved away
-- (gone from the old key). A heartbeat or a progress write touches no subject.
CREATE OR REPLACE FUNCTION processing_done_subjects_changed() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE r RECORD;
BEGIN
    FOR r IN
        WITH c AS (
            SELECT n.library_id, n.stage, n.subject_id, n.subject_kind,
                   COALESCE(n.finished_at, n.created_at) AS at, TRUE AS add
              FROM new_rows n JOIN old_rows o ON o.id = n.id
             WHERE n.state = 'done' AND n.library_id IS NOT NULL AND n.subject_id IS NOT NULL
               AND (o.state IS DISTINCT FROM 'done'
                    OR o.library_id IS DISTINCT FROM n.library_id
                    OR o.stage IS DISTINCT FROM n.stage
                    OR o.subject_id IS DISTINCT FROM n.subject_id
                    OR o.finished_at IS DISTINCT FROM n.finished_at)
            UNION ALL
            SELECT o.library_id, o.stage, o.subject_id, o.subject_kind, NULL, FALSE
              FROM old_rows o JOIN new_rows n ON n.id = o.id
             WHERE o.state = 'done' AND o.library_id IS NOT NULL AND o.subject_id IS NOT NULL
               AND (n.state IS DISTINCT FROM 'done'
                    OR n.library_id IS DISTINCT FROM o.library_id
                    OR n.stage IS DISTINCT FROM o.stage
                    OR n.subject_id IS DISTINCT FROM o.subject_id)
        )
        -- a key both added and left keeps its subject: a done row of it is there
        SELECT library_id, stage, subject_id, min(subject_kind) AS kind, max(at) AS last,
               bool_or(add) AS add
          FROM c
         GROUP BY library_id, stage, subject_id
         ORDER BY library_id, stage, subject_id
    LOOP
        PERFORM processing_done_subjects_touch(r.library_id, r.stage, r.subject_id, r.kind, r.last, r.add);
    END LOOP;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- rows deleted (the retention's batches, a removed video's cascade)
CREATE OR REPLACE FUNCTION processing_done_subjects_gone() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE r RECORD;
BEGIN
    FOR r IN
        SELECT library_id, stage, subject_id, min(subject_kind) AS kind
          FROM gone
         WHERE state = 'done' AND library_id IS NOT NULL AND subject_id IS NOT NULL
         GROUP BY library_id, stage, subject_id
         ORDER BY library_id, stage, subject_id
    LOOP
        PERFORM processing_done_subjects_touch(r.library_id, r.stage, r.subject_id, r.kind, NULL, FALSE);
    END LOOP;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER processing_done_subjects_ins_trg
    AFTER INSERT ON processing_jobs
    REFERENCING NEW TABLE AS added
    FOR EACH STATEMENT
    EXECUTE FUNCTION processing_done_subjects_added();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER processing_done_subjects_upd_trg
    AFTER UPDATE ON processing_jobs
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
    FOR EACH STATEMENT
    EXECUTE FUNCTION processing_done_subjects_changed();
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
 WHERE state = 'done' AND library_id IS NOT NULL AND subject_id IS NOT NULL
 GROUP BY library_id, stage, subject_id
 ORDER BY library_id, stage, subject_id
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
DROP TRIGGER IF EXISTS processing_done_subjects_upd_trg ON processing_jobs;
DROP TRIGGER IF EXISTS processing_done_subjects_ins_trg ON processing_jobs;
DROP FUNCTION IF EXISTS processing_done_subjects_gone();
DROP FUNCTION IF EXISTS processing_done_subjects_changed();
DROP FUNCTION IF EXISTS processing_done_subjects_added();
DROP FUNCTION IF EXISTS processing_done_subjects_touch(UUID, TEXT, UUID, TEXT, TIMESTAMPTZ, BOOLEAN);
DROP TABLE IF EXISTS processing_done_subjects;
-- +goose StatementEnd
