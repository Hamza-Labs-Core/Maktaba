package pgbundle

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Epic 36 story 36.9: a Mac that ran the developer stack moves its database into
// the bundled Postgres (maktaba-server migrate-from-dev). These run with no
// supervisor up: the caller checks that nothing listens on the port first.

// Listening reports whether a Postgres (the app's, most likely) already answers
// on this bundle's port.
func (b Bundle) Listening(ctx context.Context) bool {
	err := Ready(ctx, b.Addr(), SuperUser, "postgres")
	return err == nil || errors.Is(err, ErrStarting) || errors.Is(err, ErrRefused)
}

// RestoreOptions shape a restore.
type RestoreOptions struct {
	// Roles are the source's other roles (maktaba_worker, the machines' roles):
	// made NOLOGIN here, so the dump's grants to them restore. Members are
	// the memberships among them ({role, group}).
	Roles   []string
	Members [][2]string
	// Jobs is pg_restore's parallelism (0: half the CPUs, at least 1).
	Jobs int
}

// Restore replaces the app database with a pg_dump custom-format dump: a fresh
// database owned by the app role, the extensions and the cluster's own grants on
// Postgres's functions (clusterSQL: a function's grant is its database's, so a fresh
// database has none) made by the superuser, then every other object restored as the
// app role (so it owns them, as the migrations would have made them). The cluster is
// prepared first when it is not. After restoring, fn (when not nil) runs while the
// server is still up, for the caller's checks.
func (b Bundle) Restore(ctx context.Context, dump string, opts RestoreOptions, log io.Writer, fn func(q Querier) error) error {
	if err := b.Prepare(ctx, log); err != nil {
		return err
	}
	superPW, err := readPassword(b.passwordFile(SuperUser))
	if err != nil {
		return err
	}
	q := Querier{b: b, pw: superPW}
	return b.withServer(ctx, log, func() error {
		// a database with data there already (a --replace) is dumped first
		if rows, err := q.Query(ctx, AppDatabase, "SELECT 1 FROM pg_class WHERE relnamespace = 'public'::regnamespace AND relkind = 'r' LIMIT 1"); err != nil {
			return err
		} else if len(rows) > 0 {
			path, err := b.Backup(ctx, time.Now())
			if err != nil {
				return fmt.Errorf("back up the database there before replacing it: %w", err)
			}
			_, _ = fmt.Fprintf(log, "[postgres] the database it replaces is in %s\n", path)
		}
		if err := b.restoreInto(ctx, q, dump, opts, log); err != nil {
			return err
		}
		if fn == nil {
			return nil
		}
		return fn(q)
	})
}

// restoreInto makes the app database afresh on q's running server and restores dump
// into it (Restore's work, and RestoreRunning's), checks it whole (VerifyRestore), and
// only then lets the other logins in again. A parallel restore that fails, or comes out
// incomplete, is done again with one worker (the whole of it: the database is made afresh
// each time); one that fails again says what failed or what is missing.
//
// The outage of 2026-10-28: a joined machine kept logging in while its home's database
// was put back, and as soon as the restore had made its functions it asked for its copy
// (cluster_node_replica_grant), whose GRANTs on every table met pg_restore's own grants
// ("tuple concurrently updated"): the restore stopped with every table's rows but none of
// its indexes, keys, triggers or grants. Now no other login reaches the database while it
// is put back (CONNECT is taken from PUBLIC, and the grants of the database itself, which
// a dump does not hold, are put back at the end); the schema, the grants and the default
// privileges go in one worker at a time, the rows, the chunks and the indexes in parallel.
func (b Bundle) restoreInto(ctx context.Context, q Querier, dump string, opts RestoreOptions, log io.Writer) error {
	grants, err := b.databaseGrants(ctx, q)
	if err != nil {
		return err
	}
	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = max(1, runtime.NumCPU()/2)
	}
	err = b.restoreOnce(ctx, q, dump, opts, jobs, grants, log)
	if err != nil && ctx.Err() == nil && jobs > 1 {
		_, _ = fmt.Fprintf(log, "[postgres] the restore of %s with %d workers failed (%v): it is done again with one\n",
			dump, jobs, err)
		err = b.restoreOnce(ctx, q, dump, opts, 1, grants, log)
	}
	if err != nil {
		return err
	}
	_ = os.Remove(b.grantsFile())
	return nil
}

// restoreOnce is one try of restoreInto, with jobs workers.
func (b Bundle) restoreOnce(ctx context.Context, q Querier, dump string, opts RestoreOptions, jobs int,
	grants []string, log io.Writer) error {
	// the machines' copies decode the database through logical slots, which DROP
	// DATABASE refuses to outlive
	if err := q.Exec(ctx, "postgres", DropSlotsSQL(AppDatabase)); err != nil {
		return fmt.Errorf("drop the machines' copy slots on %s: %w", AppDatabase, err)
	}
	if err := q.Exec(ctx, "postgres", recreateSQL(opts)+clusterSQL); err != nil {
		return fmt.Errorf("make a fresh %s database: %w", AppDatabase, err)
	}
	_, _ = fmt.Fprintf(log, "[postgres] putting %s back into a fresh %s database (%d workers): no other login "+
		"reaches it until it is whole\n", dump, AppDatabase, jobs)
	toc, err := b.tableOfContents(ctx, dump, q.pw)
	if err != nil {
		return err
	}
	main, acls, err := writeRestoreLists(dump, toc)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(main); _ = os.Remove(acls) }()
	// not the dump's publications (the machines' copies, Epic 37): their slots went with the
	// database, each machine copies it anew, and its next grant makes its publication again
	// with the tables there then (cluster_copy_publication). A machine that asks meanwhile
	// made it while the tables came back, and the dump's own ALTER PUBLICATION ... ADD
	// TABLE then failed the whole restore ("is already member of publication"; the owner's
	// rollback of 2026-10-27), as the in-place restore of another Postgres never did.
	env := append(os.Environ(), "PGPASSWORD="+q.pw)
	restore := func(list string, workers int, sections ...string) error {
		args := []string{"-h", "127.0.0.1", "-p", strconv.Itoa(b.Port), "-U", SuperUser, "-d", AppDatabase, "--no-owner",
			"--role=" + AppRole, "--exit-on-error", "--no-publications", "-j", strconv.Itoa(workers), "-L", list}
		for _, s := range sections {
			args = append(args, "--section="+s)
		}
		cmd := exec.CommandContext(ctx, b.Bin("pg_restore"), append(args, dump)...)
		cmd.Env = env
		cmd.Stdout, cmd.Stderr = childLog(log), childLog(log)
		if err := cmd.Run(); err != nil {
			what := "the rows"
			switch {
			case list == acls:
				what = "the grants"
			case len(sections) == 1 && sections[0] == "pre-data":
				what = "the tables and functions"
			case len(sections) == 1 && sections[0] == "post-data":
				what = "the indexes, keys and triggers"
			}
			return fmt.Errorf("pg_restore %s (%s): %w", dump, what, err)
		}
		return nil
	}
	// the schema: one worker (its items change shared rows of the catalog)
	if err := restore(main, 1, "pre-data"); err != nil {
		return err
	}
	// the rows, in parallel: each table's own COPY
	if err := restore(main, jobs, "data"); err != nil {
		return err
	}
	// the chunked tables' rows between their tables and their indexes (dumpfmt.go)
	if chunks := Chunks(dump); len(chunks) > 0 {
		if err := LoadChunks(ctx, b.Bin("psql"), []string{"-h", "127.0.0.1", "-p", strconv.Itoa(b.Port), "-U", SuperUser,
			"--dbname=" + AppDatabase}, env, chunks, jobs); err != nil {
			return err
		}
	}
	// the indexes, keys and triggers, in parallel (pg_restore orders those of a table)
	if err := restore(main, jobs, "post-data"); err != nil {
		return err
	}
	// the grants and default privileges last, one worker: every object they name is there
	if err := restore(acls, 1); err != nil {
		return err
	}
	if err := VerifyRestore(ctx, dump, toc, q.queryFunc(AppDatabase)); err != nil {
		return fmt.Errorf("restore %s: %w", dump, err)
	}
	if err := q.Exec(ctx, AppDatabase, "ANALYZE;"); err != nil {
		return err
	}
	// whole: the other logins may use it again, with the database's own grants as they were
	_, _ = fmt.Fprintf(log, "[postgres] %s is put back whole: every table, index, key, trigger and grant, and its rows\n", dump)
	if err := q.Exec(ctx, "postgres", strings.Join(grants, "\n")); err != nil {
		return fmt.Errorf("give the database's grants back after the restore: %w", err)
	}
	return nil
}

// queryFunc is q's Query on database as a QueryFunc.
func (q Querier) queryFunc(database string) QueryFunc {
	return func(ctx context.Context, sql string) ([]string, error) { return q.Query(ctx, database, sql) }
}

// grantsFile keeps the app database's own grants (CONNECT, TEMPORARY: a dump does not hold
// them) while a restore runs, for one that is cut short and done again by the next start
// (the database then has CONNECT taken from PUBLIC).
func (b Bundle) grantsFile() string { return filepath.Join(b.Backups, ".restore-database-grants.json") }

// databaseGrants are the statements that give the app database its grants back as they
// are before the restore (the grants file a restore cut short left, else the database's
// own): those to PUBLIC (none said: the default, CONNECT and TEMPORARY) and to the other
// roles (maktaba_worker's CONNECT, which the machines' logins come through).
func (b Bundle) databaseGrants(ctx context.Context, q Querier) ([]string, error) {
	var grants []string
	if raw, err := os.ReadFile(b.grantsFile()); err == nil && json.Unmarshal(raw, &grants) == nil && len(grants) > 0 {
		return grants, nil
	}
	rows, err := q.Query(ctx, "postgres", `SELECT CASE WHEN d.datacl IS NULL THEN 'default'
		ELSE coalesce((SELECT string_agg(CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE quote_ident(r.rolname) END
		                                 || ':' || a.privilege_type, ',')
		      FROM aclexplode(d.datacl) a LEFT JOIN pg_roles r ON r.oid = a.grantee
		      WHERE a.grantee <> d.datdba), '') END
		FROM pg_database d WHERE d.datname = '`+AppDatabase+`'`)
	if err != nil {
		return nil, fmt.Errorf("read the %s database's grants: %w", AppDatabase, err)
	}
	spec := "default"
	if len(rows) > 0 {
		spec = rows[0]
	}
	if spec == "default" {
		grants = []string{"GRANT CONNECT, TEMPORARY ON DATABASE " + AppDatabase + " TO PUBLIC;"}
	} else {
		for _, g := range strings.Split(spec, ",") {
			who, priv, ok := strings.Cut(g, ":")
			if !ok || (who != "PUBLIC" && !ValidRoleName(strings.Trim(who, `"`))) {
				continue
			}
			// a role named in the grants may not be there (gone since): skipped, not failed
			stmt := "GRANT " + priv + " ON DATABASE " + AppDatabase + " TO " + who + ";"
			if who != "PUBLIC" {
				stmt = "SELECT $$" + stmt + "$$ WHERE EXISTS (SELECT FROM pg_roles WHERE rolname = '" +
					strings.Trim(who, `"`) + "')\\gexec"
			}
			grants = append(grants, stmt)
		}
	}
	raw, err := json.Marshal(grants)
	if err == nil {
		err = os.MkdirAll(b.Backups, 0o700)
	}
	if err == nil {
		err = os.WriteFile(b.grantsFile(), raw, 0o600)
	}
	if err != nil {
		return nil, fmt.Errorf("keep the %s database's grants while it is put back: %w", AppDatabase, err)
	}
	return grants, nil
}

// EndSessionsSQL ends every other client session on database db ("": the one it runs
// in) that its role may end, and selects how many it ended: the machines' api sessions
// and anything else that could hold a lock an update's migrations would wait for, right
// before they run. Replication (the machines' copies) goes on.
func EndSessionsSQL(db string) string {
	which := "current_database()"
	if db != "" {
		which = "'" + strings.ReplaceAll(db, "'", "''") + "'"
	}
	return `SELECT count(*) FROM (
    SELECT pg_terminate_backend(a.pid) AS ended
    FROM pg_stat_activity a LEFT JOIN pg_roles r ON r.oid = a.usesysid
    WHERE a.datname = ` + which + ` AND a.pid <> pg_backend_pid() AND a.backend_type = 'client backend'
      AND ((SELECT rolsuper FROM pg_roles WHERE rolname = current_user)
           OR (pg_has_role(current_user, 'pg_signal_backend', 'MEMBER') AND NOT coalesce(r.rolsuper, false))
           OR pg_has_role(current_user, a.usesysid, 'MEMBER'))
) e WHERE e.ended`
}

// EndSessions ends the other client sessions on the app database (EndSessionsSQL); it
// returns how many it ended.
func (b Bundle) EndSessions(ctx context.Context) (int, error) {
	q, err := b.NewQuerier()
	if err != nil {
		return 0, err
	}
	rows, err := q.Query(ctx, "postgres", EndSessionsSQL(AppDatabase))
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(rows[0]))
}

// DropSlotsSQL drops every logical replication slot of database db ("": the one it
// runs in), ending the sessions that stream from them first: the machines' copies
// (Epic 37) of a database about to be put back from a dump, or dropped. What they
// decoded is gone; each machine copies the database anew once its grant says its slot
// is (nodecopy, "the home gave up on this copy"). Fails when a slot stays in use.
func DropSlotsSQL(db string) string {
	which := "current_database()"
	if db != "" {
		which = "'" + strings.ReplaceAll(db, "'", "''") + "'"
	}
	where := "database = " + which + " AND slot_type = 'logical'"
	return `DO $$
DECLARE n int;
BEGIN
    FOR i IN 1..100 LOOP
        BEGIN
            PERFORM pg_terminate_backend(active_pid) FROM pg_replication_slots WHERE ` + where + ` AND active_pid IS NOT NULL;
        EXCEPTION WHEN insufficient_privilege THEN NULL;
        END;
        BEGIN
            PERFORM pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE ` + where + ` AND NOT active;
        EXCEPTION WHEN object_in_use THEN NULL; -- taken again meanwhile: the next round
        END;
        SELECT count(*) INTO n FROM pg_replication_slots WHERE ` + where + `;
        EXIT WHEN n = 0;
        PERFORM pg_sleep(0.1);
    END LOOP;
    IF n > 0 THEN
        RAISE EXCEPTION '% logical replication slot(s) still in use', n;
    END IF;
END
$$;
`
}

var roleName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// ValidRoleName: a role name Restore can make (lower case, digits, underscores).
func ValidRoleName(r string) bool { return roleName.MatchString(r) }

// Extensions are the extensions every app database has (made by Prepare and
// Restore; vector when the bundle has it); a dump that needs another cannot be
// restored as the app role.
var Extensions = []string{"plpgsql", "pg_trgm", "unaccent", "pgcrypto", "vector"}

// recreateSQL drops the app database (and anything connected to it) and makes it
// again, with the extensions and the source's other roles.
func recreateSQL(opts RestoreOptions) string {
	var sb strings.Builder
	sb.WriteString("DROP DATABASE IF EXISTS " + AppDatabase + " WITH (FORCE);\n")
	sb.WriteString("CREATE DATABASE " + AppDatabase + " OWNER " + AppRole + " TEMPLATE template0 ENCODING 'UTF8';\n")
	// no other login reaches it until it is put back whole (restoreInto gives its grants
	// back then): its owner and the superuser only, and anything that came in first goes
	sb.WriteString("REVOKE ALL ON DATABASE " + AppDatabase + " FROM PUBLIC;\n")
	sb.WriteString("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '" + AppDatabase +
		"' AND pid <> pg_backend_pid();\n")
	for _, r := range opts.Roles {
		if !ValidRoleName(r) || r == AppRole || r == SuperUser {
			continue
		}
		sb.WriteString("SELECT 'CREATE ROLE " + r + " NOLOGIN' WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '" + r + "')\\gexec\n")
	}
	for _, m := range opts.Members {
		if ValidRoleName(m[0]) && ValidRoleName(m[1]) {
			sb.WriteString("GRANT " + m[1] + " TO " + m[0] + ";\n")
		}
	}
	sb.WriteString("\\c " + AppDatabase + "\n")
	sb.WriteString("CREATE EXTENSION IF NOT EXISTS pg_trgm;\nCREATE EXTENSION IF NOT EXISTS unaccent;\nCREATE EXTENSION IF NOT EXISTS pgcrypto;\n")
	sb.WriteString(vectorSQL)
	return sb.String()
}

// tableOfContents is the dump's table of contents as it is restored: without its
// extensions (and their comments) and its grants on Postgres's own objects (pg_catalog's:
// a bundled source's clusterSQL): those need the superuser, and the database has them
// already (commented out: pg_restore -L skips them, VerifyRestore does not look for them).
func (b Bundle) tableOfContents(ctx context.Context, dump, superPW string) (string, error) {
	cmd := exec.CommandContext(ctx, b.Bin("pg_restore"), "-l", dump)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+superPW)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pg_restore -l %s: %w: %s", dump, err, strings.TrimSpace(stderr.String()))
	}
	var sb strings.Builder
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, ";") && (strings.Contains(line, " EXTENSION - ") || strings.Contains(line, " COMMENT - EXTENSION ") ||
			strings.Contains(line, " ACL pg_catalog ")) {
			line = ";" + line
		}
		sb.WriteString(line + "\n")
	}
	return sb.String(), nil
}

// writeRestoreLists writes the table of contents as two lists for pg_restore -L beside
// the dump: main, every item but the grants and default privileges (restored by section),
// and acls, those alone (restored last, once every object they name is there).
func writeRestoreLists(dump, toc string) (main, acls string, err error) {
	write := func(keep func(line string) bool) (string, error) {
		f, err := os.CreateTemp(filepath.Dir(dump), ".restore-list-*")
		if err != nil {
			return "", err
		}
		w := bufio.NewWriter(f)
		for _, line := range strings.Split(toc, "\n") {
			if line != "" && !strings.HasPrefix(line, ";") && !keep(line) {
				line = ";" + line
			}
			_, _ = w.WriteString(line + "\n")
		}
		if err := w.Flush(); err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return "", err
		}
		return f.Name(), f.Close()
	}
	if main, err = write(func(l string) bool { return !isACL(l) }); err != nil {
		return "", "", err
	}
	if acls, err = write(isACL); err != nil {
		_ = os.Remove(main)
		return "", "", err
	}
	return main, acls, nil
}

// Querier runs SQL as the superuser on a bundle's running server.
type Querier struct {
	b  Bundle
	pw string
}

// NewQuerier is a Querier for a server already running (the app's).
func (b Bundle) NewQuerier() (Querier, error) {
	pw, err := readPassword(b.passwordFile(SuperUser))
	if err != nil {
		return Querier{}, err
	}
	return Querier{b: b, pw: pw}, nil
}

// Exec runs a script (stops at the first error).
func (q Querier) Exec(ctx context.Context, database, script string) error {
	_, err := q.run(ctx, database, script)
	return err
}

// Query runs one statement and returns its rows, fields separated by '|'.
func (q Querier) Query(ctx context.Context, database, sql string) ([]string, error) {
	out, err := q.run(ctx, database, sql)
	if err != nil {
		return nil, err
	}
	var rows []string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l != "" {
			rows = append(rows, l)
		}
	}
	return rows, nil
}

func (q Querier) run(ctx context.Context, database, script string) (string, error) {
	cmd := exec.CommandContext(ctx, q.b.Bin("psql"), "-X", "-q", "-At", "-v", "ON_ERROR_STOP=1",
		"-h", "127.0.0.1", "-p", strconv.Itoa(q.b.Port), "-U", SuperUser, "-d", database)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+q.pw)
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("psql: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
