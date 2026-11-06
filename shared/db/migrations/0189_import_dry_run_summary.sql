-- +goose Up
-- +goose StatementBegin
--
-- Slot 0189 - the mediaintel import's dry run writes no done-subjects summary.
--
-- The import (pipeline tools/import_mediaintel.py) writes synthetic done jobs stage after
-- stage in one transaction: each stage's fingerprint reads the done jobs of the stages
-- before it, so they cannot go in one statement or in key order. On --apply it holds
-- processing_done_subjects (SHARE ROW EXCLUSIVE, slot 0184) while it writes them. Its
-- dry run (the default) took that lock too, so every job that finished while it ran
-- waited for it (#662 review).
--
-- processing_done_subjects_touch now returns at once when the transaction-local setting
-- maktaba.done_subjects_dry_run is 'on'. Only the import's dry run sets it (SET LOCAL),
-- and that transaction always rolls back, so a summary it does not keep is never
-- committed. The dry run then locks no summary row and needs no table lock.
--
-- Additive: one function replaced, same signature; unchanged when the setting is unset.
--
CREATE OR REPLACE FUNCTION processing_done_subjects_touch(
    p_library UUID, p_stage TEXT, p_subject UUID, p_kind TEXT, p_last TIMESTAMPTZ, p_add BOOLEAN
) RETURNS VOID
LANGUAGE plpgsql AS $$
DECLARE
    prev TEXT := current_setting('maktaba.done_subjects_key', true);
BEGIN
    -- the mediaintel import's dry run (always rolled back): no summary row is written or
    -- locked, so no finishing job waits on it and no order can break
    IF current_setting('maktaba.done_subjects_dry_run', true) = 'on' THEN
        RETURN;
    END IF;
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
-- dev only (README rule 6): slot 0184's body
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
