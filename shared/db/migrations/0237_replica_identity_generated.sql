-- +goose Up
--
-- Slot 0237: a published table whose replica identity holds a generated column takes
-- updates again on PostgreSQL 18.
--
-- A machine's copy publishes every copied table (slot 0147, cluster_copy_publication) with
-- the publication's default publish_generated_columns = none: a generated column is never
-- sent (the machine computes its own). PostgreSQL 18 refuses an UPDATE or a DELETE on a
-- published table whose replica identity includes a generated column the publication does
-- not publish ("Replica identity must not contain unpublished generated columns",
-- SQLSTATE 42P10); PostgreSQL 16 ignored generated columns there. A table with
-- REPLICA IDENTITY FULL has every column in its identity, so a FULL table with a generated
-- column can no longer be updated once it is published.
--
-- The home that moved from 16 to 18 on 2026-11-20 had transcript_segments at REPLICA
-- IDENTITY FULL (149 tables with a primary key are FULL there; no migration sets FULL on a
-- table with a primary key) and search_tsv, the stored generated tsvector of slot 0016. From
-- the upgrade on, every second_listen job failed at its UPDATE of transcript_segments.
--
-- This puts every such table (in public, FULL, with a primary key and a generated column)
-- back to REPLICA IDENTITY DEFAULT, the primary key: the identity every migration gives
-- these tables, and the one a fresh home and CI have. A machine's copy then matches rows by
-- the key, which its own tables have from the same migrations. A table without a primary
-- key is not touched (DEFAULT would leave it with no identity at all).
--
-- The change is to the catalog only: no rewrite, no row read. ALTER TABLE takes the table's
-- ACCESS EXCLUSIVE lock for that moment; a writer in the way makes it wait at most 2 s
-- (lock_timeout) and try again, 30 times, so the queries behind it are never held longer.
-- A table already DEFAULT (every other home, CI) is left alone, so a second run does
-- nothing.
--
-- +goose StatementBegin
DO $$
DECLARE
    v_tab regclass;
    v_try integer;
BEGIN
    PERFORM set_config('lock_timeout', '2s', true);
    FOR v_tab IN
        SELECT c.oid::regclass FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
         WHERE n.nspname = 'public' AND c.relkind = 'r' AND c.relreplident = 'f'
           AND EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.oid AND i.indisprimary)
           AND EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attnum > 0
                          AND NOT a.attisdropped AND a.attgenerated <> '')
         ORDER BY c.relname
    LOOP
        v_try := 0;
        LOOP
            BEGIN
                EXECUTE format('ALTER TABLE %s REPLICA IDENTITY DEFAULT', v_tab);
                EXIT;
            EXCEPTION WHEN lock_not_available THEN
                v_try := v_try + 1;
                IF v_try >= 30 THEN
                    RAISE;
                END IF;
                PERFORM pg_sleep(1);
            END;
        END LOOP;
        RAISE NOTICE 'slot 0237: % has REPLICA IDENTITY DEFAULT (its primary key)', v_tab;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
--
-- Nothing is put back. The Up changed only the replica identity of tables that had FULL
-- with a generated column; DEFAULT (the primary key) is what every migration before this
-- one gives them, and every release's code works with it. Making them FULL again would
-- only stop their updates again on PostgreSQL 18. The Up runs in one transaction, so a
-- failed Up leaves nothing for a Down to undo either. Running this after a partial Up, or
-- twice, is harmless.
--
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
