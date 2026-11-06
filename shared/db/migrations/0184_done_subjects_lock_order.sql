-- +goose Up
-- +goose StatementBegin
--
-- Slot 0184 - processing_done_subjects: the lock order is checked across a transaction.
--
-- Slot 0183's triggers lock the summary rows of a statement's subjects in key order
-- (library_id, stage, subject_id), so two statements never wait on each other in a
-- circle. That holds per statement only: a transaction that finishes subject B and, in a
-- later statement, subject A holds B while it waits for A, and a retention batch that
-- takes A and then B can deadlock with it (#642). No caller does that today.
--
-- processing_done_subjects_touch now remembers, for the rest of the transaction, the
-- highest key it has locked (a transaction-local setting, maktaba.done_subjects_key). A
-- later touch with a lower key breaks the order: it raises when the session has
-- maktaba.lock_order_strict = 'on' (the test databases and CI set it, so a new code path
-- that does it fails its tests) and warns otherwise (a home keeps running; the warning
-- names the subjects in its log). Keys taken in order across statements are fine, and so
-- is any order in a transaction that holds the whole table (LOCK TABLE
-- processing_done_subjects IN SHARE ROW EXCLUSIVE MODE, as the mediaintel import does:
-- no other writer of the summary runs beside it). Only a touch out of order looks at
-- pg_locks.
--
-- Additive: one function replaced, same signature and same writes.
--
CREATE OR REPLACE FUNCTION processing_done_subjects_touch(
    p_library UUID, p_stage TEXT, p_subject UUID, p_kind TEXT, p_last TIMESTAMPTZ, p_add BOOLEAN
) RETURNS VOID
LANGUAGE plpgsql AS $$
DECLARE
    prev TEXT := current_setting('maktaba.done_subjects_key', true);
BEGIN
    IF prev IS NOT NULL AND prev <> ''
       AND (p_library, p_stage, p_subject)
           < (split_part(prev, '|', 1)::uuid, split_part(prev, '|', 2), split_part(prev, '|', 3)::uuid)
       -- a transaction that holds the whole table (LOCK TABLE ... SHARE ROW EXCLUSIVE or
       -- stronger: the mediaintel import) waits on no other writer's row: any order is fine
       AND NOT EXISTS (SELECT 1 FROM pg_locks l
                        WHERE l.locktype = 'relation' AND l.pid = pg_backend_pid() AND l.granted
                          AND l.relation = 'processing_done_subjects'::regclass
                          AND l.mode IN ('ShareRowExclusiveLock', 'ExclusiveLock', 'AccessExclusiveLock'))
    THEN
        IF current_setting('maktaba.lock_order_strict', true) = 'on' THEN
            RAISE EXCEPTION 'processing_done_subjects: % / % / % locked after % in one transaction (lock order, slot 0184)',
                p_library, p_stage, p_subject, prev
                USING ERRCODE = 'P0001', HINT = 'finish or delete these jobs in one statement, or in key order';
        END IF;
        RAISE WARNING 'processing_done_subjects: % / % / % locked after % in one transaction (lock order, slot 0184)',
            p_library, p_stage, p_subject, prev;
    END IF;
    IF p_add THEN
        INSERT INTO processing_done_subjects AS d (library_id, stage, subject_id, subject_kind, last_at)
        VALUES (p_library, p_stage, p_subject, p_kind, p_last)
        ON CONFLICT (library_id, stage, subject_id) DO UPDATE
            SET last_at = EXCLUDED.last_at
          WHERE d.last_at < EXCLUDED.last_at;
    ELSE
        PERFORM 1 FROM processing_done_subjects d
          WHERE d.library_id = p_library AND d.stage = p_stage AND d.subject_id = p_subject
            FOR UPDATE;
        IF NOT FOUND THEN
            RETURN;  -- nothing locked
        END IF;
        DELETE FROM processing_done_subjects d
         WHERE d.library_id = p_library AND d.stage = p_stage AND d.subject_id = p_subject
           AND NOT EXISTS (SELECT 1 FROM processing_jobs j
                            WHERE j.subject_kind = p_kind AND j.subject_id = p_subject
                              AND j.stage = p_stage AND j.state = 'done'
                              AND j.library_id = p_library);
    END IF;
    IF prev IS NULL OR prev = ''
       OR (p_library, p_stage, p_subject)
          > (split_part(prev, '|', 1)::uuid, split_part(prev, '|', 2), split_part(prev, '|', 3)::uuid)
    THEN
        PERFORM set_config('maktaba.done_subjects_key',
                           p_library::text || '|' || p_stage || '|' || p_subject::text, true);
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- dev only (README rule 6): slot 0183's body
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
