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

// restoreInto makes the app database afresh on q's running server and restores
// dump into it (Restore's work, and RestoreRunning's).
func (b Bundle) restoreInto(ctx context.Context, q Querier, dump string, opts RestoreOptions, log io.Writer) error {
	// the machines' copies decode the database through logical slots, which DROP
	// DATABASE refuses to outlive
	if err := q.Exec(ctx, "postgres", DropSlotsSQL(AppDatabase)); err != nil {
		return fmt.Errorf("drop the machines' copy slots on %s: %w", AppDatabase, err)
	}
	if err := q.Exec(ctx, "postgres", recreateSQL(opts)+clusterSQL); err != nil {
		return fmt.Errorf("make a fresh %s database: %w", AppDatabase, err)
	}
	list, err := b.restoreList(ctx, dump, q.pw)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(list) }()
	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = max(1, runtime.NumCPU()/2)
	}
	cmd := exec.CommandContext(ctx, b.Bin("pg_restore"), "-h", "127.0.0.1", "-p", strconv.Itoa(b.Port),
		"-U", SuperUser, "-d", AppDatabase, "--no-owner", "--role="+AppRole, "--exit-on-error",
		"-j", strconv.Itoa(jobs), "-L", list, dump)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+q.pw)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore %s: %w", dump, err)
	}
	return q.Exec(ctx, AppDatabase, "ANALYZE;")
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
// Restore); a dump that needs another cannot be restored as the app role.
var Extensions = []string{"plpgsql", "pg_trgm", "unaccent", "pgcrypto"}

// recreateSQL drops the app database (and anything connected to it) and makes it
// again, with the extensions and the source's other roles.
func recreateSQL(opts RestoreOptions) string {
	var sb strings.Builder
	sb.WriteString("DROP DATABASE IF EXISTS " + AppDatabase + " WITH (FORCE);\n")
	sb.WriteString("CREATE DATABASE " + AppDatabase + " OWNER " + AppRole + " TEMPLATE template0 ENCODING 'UTF8';\n")
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
	return sb.String()
}

// restoreList writes the dump's table of contents without its extensions (and
// their comments) and its grants on Postgres's own objects (pg_catalog's: a bundled
// source's clusterSQL): those need the superuser, and the database has them already.
func (b Bundle) restoreList(ctx context.Context, dump, superPW string) (string, error) {
	cmd := exec.CommandContext(ctx, b.Bin("pg_restore"), "-l", dump)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+superPW)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pg_restore -l %s: %w: %s", dump, err, strings.TrimSpace(stderr.String()))
	}
	f, err := os.CreateTemp(filepath.Dir(dump), ".restore-list-*")
	if err != nil {
		return "", err
	}
	w := bufio.NewWriter(f)
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, ";") && (strings.Contains(line, " EXTENSION - ") || strings.Contains(line, " COMMENT - EXTENSION ") ||
			strings.Contains(line, " ACL pg_catalog ")) {
			line = ";" + line
		}
		_, _ = w.WriteString(line + "\n")
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return "", err
	}
	return f.Name(), f.Close()
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
