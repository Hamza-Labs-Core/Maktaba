package pgbundle

import (
	"bufio"
	"bytes"
	"context"
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

// The databases of a restore: the dump is put back into restoringDatabase, which no
// client knows the name of and no login but the superuser's and the app role's may enter,
// checked whole there, and renamed into place in one transaction; the database it replaces
// is renamed replacedDatabase in that same transaction and dropped after it.
const (
	restoringDatabase = AppDatabase + "_restoring"
	replacedDatabase  = AppDatabase + "_replaced"
)

// restoreInto puts dump back as the app database on q's running server (Restore's work,
// and RestoreRunning's): into a scratch database of its own, checked whole there
// (VerifyRestore), then renamed into place with the database's own grants. A parallel try
// that fails, or comes out incomplete, is done again with one worker; one that fails again
// says what failed or what is missing, and leaves the app database as it was.
//
// The outage of 2026-10-28: a joined machine kept logging in while its home's database
// was put back, and as soon as the restore had made its functions it asked for its copy
// (cluster_node_replica_grant), whose GRANTs on every table met pg_restore's own grants
// ("tuple concurrently updated"): the restore stopped with every table's rows but none of
// its indexes, keys, triggers or grants. A login kept out of the fresh database is not
// enough (the review of #433): the app role owns it, and processes that run on across a
// restart (the TV slots handed over, story 35.7) log in as it. Under a name nobody uses,
// nothing but the restore reaches it; whatever they write meanwhile goes to the database
// being replaced, as it would have before the rollback. The schema, the grants and the
// default privileges go in one worker at a time, the rows, the chunks and the indexes in
// parallel.
func (b Bundle) restoreInto(ctx context.Context, q Querier, dump string, opts RestoreOptions, log io.Writer) error {
	// a damaged dump is refused before anything is touched
	if err := CheckCounts(dump); err != nil {
		return fmt.Errorf("restore %s (nothing changed): %w", dump, err)
	}
	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = max(1, runtime.NumCPU()/2)
	}
	if err := b.makeRoomBeside(ctx, q, dump, log); err != nil {
		return err
	}
	err := b.restoreOnce(ctx, q, dump, opts, jobs, log)
	if err != nil && ctx.Err() == nil && jobs > 1 {
		_, _ = fmt.Fprintf(log, "[postgres] the restore of %s with %d workers failed (%v): it is done again with one\n",
			dump, jobs, err)
		err = b.restoreOnce(ctx, q, dump, opts, 1, log)
	}
	if err != nil {
		// the scratch database goes; the app database is the one before the restore
		_ = q.Exec(context.WithoutCancel(ctx), "postgres", "DROP DATABASE IF EXISTS "+restoringDatabase+" WITH (FORCE);")
		return err
	}
	return nil
}

// databaseExists reports whether database db is on q's server.
func (q Querier) databaseExists(ctx context.Context, db string) (bool, error) {
	rows, err := q.Query(ctx, "postgres", "SELECT 1 FROM pg_database WHERE datname = '"+db+"'")
	return len(rows) > 0, err
}

// roomFor is how much room the scratch database wants beside the one it replaces: the
// size of the one it replaces, with a quarter more (its indexes are built afresh) and a
// GiB.
func roomFor(size int64) uint64 { return uint64(size)*5/4 + 1<<30 }

// makeRoomBeside makes sure the scratch database fits beside the app database; when it
// does not, the app database (whose state the restore replaces anyway) is dropped first,
// with the machines' copy slots (logged: a restore that fails then leaves no database).
func (b Bundle) makeRoomBeside(ctx context.Context, q Querier, dump string, log io.Writer) error {
	// what a restore before this one left
	if err := q.Exec(ctx, "postgres", "DROP DATABASE IF EXISTS "+replacedDatabase+" WITH (FORCE);\n"+
		"DROP DATABASE IF EXISTS "+restoringDatabase+" WITH (FORCE);"); err != nil {
		return fmt.Errorf("drop what a restore before left: %w", err)
	}
	there, err := q.databaseExists(ctx, AppDatabase)
	if err != nil || !there {
		return err
	}
	rows, err := q.Query(ctx, "postgres", "SELECT pg_database_size('"+AppDatabase+"')")
	if err != nil || len(rows) == 0 {
		return err
	}
	size, _ := strconv.ParseInt(strings.TrimSpace(rows[0]), 10, 64)
	free, _, err := diskSpaceOf(b.Data)
	if err != nil || free >= roomFor(size) {
		return nil
	}
	_, _ = fmt.Fprintf(log, "[postgres] level=warn not enough room to put %s back beside the %s database (%d MB free, "+
		"%d MB wanted): the database it replaces is dropped first\n", dump, AppDatabase, free>>20, roomFor(size)>>20)
	if err := q.Exec(ctx, "postgres", DropSlotsSQL(AppDatabase)); err != nil {
		return fmt.Errorf("drop the machines' copy slots on %s: %w", AppDatabase, err)
	}
	return q.Exec(ctx, "postgres", "DROP DATABASE IF EXISTS "+AppDatabase+" WITH (FORCE);")
}

// restoreOnce is one try of restoreInto, with jobs workers.
func (b Bundle) restoreOnce(ctx context.Context, q Querier, dump string, opts RestoreOptions, jobs int,
	log io.Writer) error {
	if err := q.Exec(ctx, "postgres", recreateSQL(opts, restoringDatabase)+clusterSQLFor(restoringDatabase)); err != nil {
		return fmt.Errorf("make a fresh %s database: %w", restoringDatabase, err)
	}
	_, _ = fmt.Fprintf(log, "[postgres] putting %s back into a fresh database of its own (%s, %d workers): no other "+
		"process reaches it until it is whole\n", dump, restoringDatabase, jobs)
	toc, err := b.tableOfContents(ctx, dump, q.pw)
	if err != nil {
		return err
	}
	main, acls, err := writeRestoreLists(dump, toc)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(main); _ = os.Remove(acls) }()
	// not the dump's publications (the machines' copies, Epic 37): their slots go with the
	// database replaced, each machine copies it anew, and its next grant makes its
	// publication again with the tables there then (cluster_copy_publication). The dump's
	// own ALTER PUBLICATION ... ADD TABLE failed the whole restore once ("is already member
	// of publication"; the owner's rollback of 2026-10-27).
	env := append(os.Environ(), "PGPASSWORD="+q.pw)
	restore := func(list string, workers int, sections ...string) error {
		args := []string{"-h", "127.0.0.1", "-p", strconv.Itoa(b.Port), "-U", SuperUser, "-d", restoringDatabase,
			"--no-owner", "--role=" + AppRole, "--exit-on-error", "--no-publications", "-j", strconv.Itoa(workers), "-L", list}
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
			"--dbname=" + restoringDatabase}, env, chunks, jobs); err != nil {
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
	if err := VerifyRestore(ctx, dump, toc, q.queryFunc(restoringDatabase)); err != nil {
		return fmt.Errorf("restore %s: %w", dump, err)
	}
	if err := q.Exec(ctx, restoringDatabase, "ANALYZE;"); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(log, "[postgres] %s is put back whole: every table, index, key, trigger and grant, and its rows\n", dump)
	return b.swapIn(ctx, q, log)
}

// swapIn renames the whole scratch database into place as the app database, with the app
// database's own grants (CONNECT, TEMPORARY: a dump never holds them; the workers' group's
// CONNECT is the machines' way in), in one transaction, and drops the one it replaced
// (with the machines' copy slots, which DROP DATABASE refuses to outlive). The database
// replaced takes no new session meanwhile; those on it are ended.
func (b Bundle) swapIn(ctx context.Context, q Querier, log io.Writer) error {
	there, err := q.databaseExists(ctx, AppDatabase)
	if err != nil {
		return err
	}
	grants := []string{"GRANT CONNECT, TEMPORARY ON DATABASE " + AppDatabase + " TO PUBLIC;"}
	if there {
		if grants, err = databaseGrants(ctx, q); err != nil {
			return err
		}
		if err := q.Exec(ctx, "postgres", DropSlotsSQL(AppDatabase)); err != nil {
			return fmt.Errorf("drop the machines' copy slots on %s: %w", AppDatabase, err)
		}
		if err := q.Exec(ctx, "postgres", "ALTER DATABASE "+AppDatabase+" ALLOW_CONNECTIONS false;\n"+
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '"+AppDatabase+
			"' AND pid <> pg_backend_pid();"); err != nil {
			return fmt.Errorf("close the %s database to new sessions: %w", AppDatabase, err)
		}
	}
	var sb strings.Builder
	sb.WriteString("BEGIN;\n")
	if there {
		sb.WriteString("ALTER DATABASE " + AppDatabase + " RENAME TO " + replacedDatabase + ";\n")
	}
	sb.WriteString("ALTER DATABASE " + restoringDatabase + " RENAME TO " + AppDatabase + ";\n")
	sb.WriteString(strings.Join(grants, "\n") + "\n")
	sb.WriteString("COMMIT;\n")
	if err := q.Exec(ctx, "postgres", sb.String()); err != nil {
		if there {
			_ = q.Exec(context.WithoutCancel(ctx), "postgres", "ALTER DATABASE "+AppDatabase+" ALLOW_CONNECTIONS true;")
		}
		return fmt.Errorf("put the restored database in place of %s: %w", AppDatabase, err)
	}
	if there {
		if err := q.Exec(ctx, "postgres", "DROP DATABASE IF EXISTS "+replacedDatabase+" WITH (FORCE);"); err != nil {
			// in place all the same; the next restore drops it
			_, _ = fmt.Fprintf(log, "[postgres] level=warn the database the restore replaced (%s) could not be dropped: %v\n",
				replacedDatabase, err)
		}
	}
	return nil
}

// queryFunc is q's Query on database as a QueryFunc.
func (q Querier) queryFunc(database string) QueryFunc {
	return func(ctx context.Context, sql string) ([]string, error) { return q.Query(ctx, database, sql) }
}

// databaseGrants are the statements that give the restored app database the grants of
// the one it replaces: those to PUBLIC (none said: the default, CONNECT and TEMPORARY) and
// to the other roles (the workers' group's CONNECT, which the machines' logins come
// through).
func databaseGrants(ctx context.Context, q Querier) ([]string, error) {
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
		return []string{"GRANT CONNECT, TEMPORARY ON DATABASE " + AppDatabase + " TO PUBLIC;"}, nil
	}
	var grants []string
	for _, g := range strings.Split(spec, ",") {
		who, priv, ok := strings.Cut(g, ":")
		if !ok || (who != "PUBLIC" && !ValidRoleName(strings.Trim(who, `"`))) {
			continue
		}
		grants = append(grants, "GRANT "+priv+" ON DATABASE "+AppDatabase+" TO "+who+";")
	}
	return grants, nil
}

// GrantWorkersConnectSQL gives the workers' group (the machines' logins) CONNECT on the
// database it runs in when the group is there: what a restore by a release before
// 2026-10-28 dropped with the database (its dump never held it), and a start finds missing.
const GrantWorkersConnectSQL = `DO $$
BEGIN
    -- its own grant (PUBLIC's CONNECT may be taken away; the machines' must not go with it)
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = '` + WorkerGroup + `')
       AND NOT EXISTS (SELECT FROM pg_database d, aclexplode(d.datacl) a
                        WHERE d.datname = current_database() AND a.privilege_type = 'CONNECT'
                          AND a.grantee = '` + WorkerGroup + `'::regrole) THEN
        EXECUTE format('GRANT CONNECT ON DATABASE %I TO ` + WorkerGroup + `', current_database());
    END IF;
END
$$;`

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

// clusterSQLFor is clusterSQL made in database db: its grants on Postgres's own functions
// (the slots' to the app role alone, pg_reload_conf) belong to a database, and clusterSQL
// connects to the app database itself, which a restore's scratch database is not yet.
func clusterSQLFor(db string) string {
	return strings.Replace(clusterSQL, "\\c "+AppDatabase+"\n", "\\c "+db+"\n", 1)
}

// recreateSQL drops database db (and anything connected to it) and makes it again, owned
// by the app role, with the extensions and the source's other roles: a restore's scratch
// database (restoringDatabase), which no client knows the name of, and no login but its
// owner's and the superuser's may enter (restoreInto renames it into place once whole).
func recreateSQL(opts RestoreOptions, db string) string {
	var sb strings.Builder
	sb.WriteString("DROP DATABASE IF EXISTS " + db + " WITH (FORCE);\n")
	sb.WriteString("CREATE DATABASE " + db + " OWNER " + AppRole + " TEMPLATE template0 ENCODING 'UTF8';\n")
	sb.WriteString("REVOKE ALL ON DATABASE " + db + " FROM PUBLIC;\n")
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
	sb.WriteString("\\c " + db + "\n")
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
