// Package dbfence stops the app's roles writing the library database, and lets them
// write again (Epic 37, specs/epics/37-node-replica): a home frozen for a switch of the
// home, or fenced once it learned of a newer term. A role's own BEGIN READ WRITE
// overrides the database's default_transaction_read_only (lib/pq sends one with every
// transaction), so only a missing right stops its writes: Take takes the rights to
// change the app's tables (INSERT, UPDATE, DELETE, TRUNCATE on each table of the public
// schema) from every role but the superusers, and records them in the same statement in
// a schema no app role may use (maktaba_fence; the machines' publications list public
// tables only, so it never leaves this database); Release gives back exactly what was
// taken. The tables' owner (the app's own role) could give those rights back to itself,
// or change the schema, so while the rights are taken an event trigger of this database
// (maktaba_fence) also refuses every DDL command, GRANT and REVOKE of a role but the
// superusers: a migration's statements, in a transaction or not, change nothing. A
// subscription applies all the same: it writes as its owner, the superuser (nodecopy's
// run_as_owner). The superuser runs both.
package dbfence

import (
	"context"
	"database/sql"
)

// Execer runs a statement (*sql.DB, *sql.Tx, *sql.Conn).
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Queryer reads a row (*sql.DB, *sql.Tx, *sql.Conn).
type Queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// takeSQL records each right before it takes it; a right taken already is recorded once.
const takeSQL = `DO $$
DECLARE r record;
BEGIN
    CREATE SCHEMA IF NOT EXISTS maktaba_fence;
    REVOKE ALL ON SCHEMA maktaba_fence FROM PUBLIC;
    CREATE TABLE IF NOT EXISTS maktaba_fence.writes (
        tbl text NOT NULL,
        grantee text NOT NULL,
        privilege text NOT NULL,
        grantable boolean NOT NULL,
        PRIMARY KEY (tbl, grantee, privilege)
    );
    FOR r IN
        SELECT format('%I.%I', n.nspname, c.relname) AS tbl,
               CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE quote_ident(g.rolname) END AS grantee,
               a.privilege_type AS privilege, a.is_grantable AS grantable
          FROM pg_class c
          JOIN pg_namespace n ON n.oid = c.relnamespace
          CROSS JOIN LATERAL aclexplode(coalesce(c.relacl, acldefault('r', c.relowner))) a
          LEFT JOIN pg_roles g ON g.oid = a.grantee
         WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
           AND a.privilege_type IN ('INSERT', 'UPDATE', 'DELETE', 'TRUNCATE')
           AND (a.grantee = 0 OR NOT g.rolsuper)
    LOOP
        INSERT INTO maktaba_fence.writes VALUES (r.tbl, r.grantee, r.privilege, r.grantable)
            ON CONFLICT DO NOTHING;
        EXECUTE format('REVOKE %s ON TABLE %s FROM %s', r.privilege, r.tbl, r.grantee);
    END LOOP;
    CREATE OR REPLACE FUNCTION maktaba_fence.refuse() RETURNS event_trigger LANGUAGE plpgsql AS $f$
    BEGIN
        IF NOT coalesce((SELECT rolsuper FROM pg_roles WHERE rolname = current_user), false) THEN
            RAISE EXCEPTION 'this library database is fenced: % is refused', tg_tag
                USING ERRCODE = 'insufficient_privilege';
        END IF;
    END
    $f$;
    IF NOT EXISTS (SELECT 1 FROM pg_event_trigger WHERE evtname = 'maktaba_fence') THEN
        CREATE EVENT TRIGGER maktaba_fence ON ddl_command_start EXECUTE FUNCTION maktaba_fence.refuse();
    END IF;
END
$$`

// releaseSQL lets the app's roles change the schema again, gives each right back but
// those of a table or a role gone since (a revoked machine's), then forgets them all.
const releaseSQL = `DO $$
DECLARE r record;
BEGIN
    DROP EVENT TRIGGER IF EXISTS maktaba_fence;
    IF to_regclass('maktaba_fence.writes') IS NULL THEN
        RETURN;
    END IF;
    FOR r IN SELECT w.tbl, w.grantee, w.privilege, w.grantable FROM maktaba_fence.writes w LOOP
        IF to_regclass(r.tbl) IS NOT NULL
           AND (r.grantee = 'PUBLIC' OR EXISTS (SELECT 1 FROM pg_roles WHERE quote_ident(rolname) = r.grantee)) THEN
            EXECUTE format('GRANT %s ON TABLE %s TO %s%s', r.privilege, r.tbl, r.grantee,
                CASE WHEN r.grantable THEN ' WITH GRANT OPTION' ELSE '' END);
        END IF;
    END LOOP;
    DELETE FROM maktaba_fence.writes;
END
$$`

// Take takes the app's roles' rights to write the public tables, and records them. Taken
// again, it takes what was given since (none: the record is the same). ex must write
// (the database may be read-only by default: the caller's transaction is READ WRITE).
func Take(ctx context.Context, ex Execer) error {
	_, err := ex.ExecContext(ctx, takeSQL)
	return err
}

// Release gives back what Take took, and forgets it; nothing taken, it does nothing.
func Release(ctx context.Context, ex Execer) error {
	_, err := ex.ExecContext(ctx, releaseSQL)
	return err
}

// Taken reports whether rights are held back (Take ran, Release did not since).
func Taken(ctx context.Context, q Queryer) (bool, error) {
	var has, trigger bool
	if err := q.QueryRowContext(ctx, `SELECT to_regclass('maktaba_fence.writes') IS NOT NULL,
	    EXISTS (SELECT 1 FROM pg_event_trigger WHERE evtname = 'maktaba_fence')`).Scan(&has, &trigger); err != nil || !has {
		return trigger, err
	}
	if trigger {
		return true, nil
	}
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM maktaba_fence.writes`).Scan(&n)
	return n > 0, err
}
