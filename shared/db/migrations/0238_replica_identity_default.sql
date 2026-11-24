-- +goose NO TRANSACTION
-- +goose Up
--
-- Slot 0238: every table with a primary key has REPLICA IDENTITY DEFAULT (its primary key).
--
-- No migration sets REPLICA IDENTITY FULL on a table with a primary key: slot 0147's
-- cluster_copy_publication, and the switchover's fork publication, set it only on a table
-- without one, which a publication needs to take its updates. A home can still have FULL
-- on such tables: the home moved to PostgreSQL 18 on 2026-11-20 had 150 of them. FULL
-- writes the whole old row to the WAL with every UPDATE and DELETE, and on PostgreSQL 18 a
-- published FULL table whose columns include a generated one takes no update at all (slot
-- 0237 put those back, transcript_segments among them). This puts back the rest.
--
-- Every table in public at FULL with a primary key goes to DEFAULT. A table without one keeps
-- FULL: without it, a published table could not be updated. A table at USING INDEX or
-- NOTHING is not touched.
--
-- The change is to the catalog only: no rewrite, no row read. Each table is its own
-- transaction (the file runs without one, and the block commits after each table), so its
-- ACCESS EXCLUSIVE lock lasts that moment and is never held together with the others'. A
-- writer in the way makes it wait at most 2 s (lock_timeout) and try again, 30 times, so the
-- queries behind it are never held longer. A crash part way leaves some tables done: the file
-- runs again whole, and a table already DEFAULT is not chosen again.
--
-- +goose StatementBegin
DO $$
DECLARE
    v_tabs regclass[];
    v_tab  regclass;
    v_try  integer;
BEGIN
    SELECT coalesce(array_agg(c.oid::regclass ORDER BY c.relname), '{}') INTO v_tabs
      FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'public' AND c.relkind = 'r' AND c.relreplident = 'f'
       AND EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.oid AND i.indisprimary);
    FOREACH v_tab IN ARRAY v_tabs LOOP
        v_try := 0;
        LOOP
            PERFORM set_config('lock_timeout', '2s', true);
            BEGIN
                -- still FULL with a primary key (another session may have changed it since)
                IF EXISTS (SELECT 1 FROM pg_class c WHERE c.oid = v_tab AND c.relreplident = 'f')
                   AND EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = v_tab AND i.indisprimary) THEN
                    EXECUTE format('ALTER TABLE %s REPLICA IDENTITY DEFAULT', v_tab);
                END IF;
                EXIT;
            EXCEPTION WHEN lock_not_available THEN
                v_try := v_try + 1;
                IF v_try >= 30 THEN
                    RAISE;
                END IF;
            END;
            PERFORM pg_sleep(1);
        END LOOP;
        COMMIT;
    END LOOP;
    RAISE NOTICE 'slot 0238: % table(s) with a primary key moved from REPLICA IDENTITY FULL to DEFAULT',
        cardinality(v_tabs);
END
$$;
-- +goose StatementEnd

-- +goose Down
--
-- Nothing is put back, after a whole Up or a partial one. The Up changed only the replica
-- identity of tables with a primary key, from FULL to DEFAULT: the identity every migration
-- gives them, and the one a fresh home and CI have; every release's code works with it. FULL
-- on them only costs WAL, and on PostgreSQL 18 stops the updates of one that gains a generated
-- column. Running this twice, or after a crash part way through the Up, is harmless.
--
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
