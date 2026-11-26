// Package dbsafe is the home's database safety around an update (Epic 35 story 35.5,
// install-kinds.md "The database"): a dump checked to read back (pg_restore --list
// names every table), a rehearsal of a release's migrations on a scratch copy restored
// from such a dump, and — for a Postgres the home does not bundle — putting a dump
// back. It runs the server's own pg_dump / pg_restore (the bundled Postgres's, or those
// of the server's major on PATH) against a database URL.
package dbsafe

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dsn"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/lib/pq" // the postgres driver
)

// Target is one Postgres database.
type Target struct {
	// URL is the database's URL (postgres://user:pass@host:port/db?sslmode=…), of a role
	// that can dump it and create databases (the bundled superuser; an operator's
	// database owner).
	URL string
	// Bin is the path of a PostgreSQL program (pg_dump, pg_restore); nil: PATH.
	Bin func(name string) string
	// Backups is the directory dumps are written to (mode 0700, never the share).
	Backups string
	// Data is the Postgres server's data directory when it is this machine's (the
	// bundled one): a rehearsal's scratch copy lands there. "": another server's.
	Data string
	// Free is the free space of the disk holding a path (tests replace it).
	Free func(path string) uint64
}

// headroom is what the dump and the rehearsal leave free on a disk.
const headroom = 1 << 30

// Space checks there is room for the update's database work before it starts (design
// §5.3 step 1): the dump (as large as the database at most) beside the backups, and
// for a rehearsal (rehearse, a database up to maxGB) its scratch copy on the server's
// disk, each with 1 GiB to spare; running a disk full takes the live Postgres down. A
// server that is not this machine's is not judged for the scratch copy.
func (t Target) Space(ctx context.Context, rehearse bool, maxGB int) error {
	size, err := t.Size(ctx)
	if err != nil {
		return fmt.Errorf("db_unreachable: %w", err)
	}
	free := t.Free
	if free == nil {
		free = hostinfo.FreeDisk
	}
	need := map[string]uint64{}
	disk := func(path string) string { return strings.ToLower(filepath.VolumeName(path) + "|" + mountOf(path)) }
	need[disk(t.Backups)] += uint64(size) + headroom
	scratch := rehearse && t.Data != "" && (maxGB <= 0 || size <= int64(maxGB)<<30)
	if scratch {
		need[disk(t.Data)] += uint64(size) + headroom
	}
	check := func(path string) error {
		have := free(path)
		want := need[disk(path)]
		if have > 0 && have < want {
			return fmt.Errorf("disk_space: %s has %s free; the update's database work needs %s (the database is %s): "+
				"free some space and the update goes on", path, gib(have), gib(want), gib(uint64(size)))
		}
		return nil
	}
	if err := check(t.Backups); err != nil {
		return err
	}
	if scratch {
		return check(t.Data)
	}
	return nil
}

// mountOf is the nearest existing folder of path (disks are told apart by their free
// space only where the OS says nothing more; the same folder chain means the same disk).
func mountOf(path string) string {
	for p := filepath.Clean(path); ; {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return deviceOf(p, info)
		}
		up := filepath.Dir(p)
		if up == p {
			return p
		}
		p = up
	}
}

func gib(n uint64) string { return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30)) }

func (t Target) bin(name string) string {
	if t.Bin != nil {
		if p := t.Bin(name); p != "" {
			return p
		}
	}
	if runtimeExe != "" {
		name += runtimeExe
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

// libpqKeys are the URL parameters libpq (pg_dump, pg_restore) knows; others
// (lib/pq's run-time settings) are dropped for the programs.
var libpqKeys = map[string]bool{"sslmode": true, "sslrootcert": true, "sslcert": true, "sslkey": true,
	"connect_timeout": true, "application_name": true, "options": true, "target_session_attrs": true,
	"passfile": true, "sslpassword": true, "gssencmode": true, "krbsrvname": true}

// parts splits the URL into the programs' connection string (without its password)
// and the password (PGPASSWORD).
func (t Target) parts(db string) (conn, password string, err error) {
	u, err := url.Parse(t.URL)
	if err != nil {
		return "", "", fmt.Errorf("the database URL: %w", err)
	}
	if u.User != nil {
		password, _ = u.User.Password()
		u.User = url.User(u.User.Username())
	}
	q := url.Values{}
	for k, v := range u.Query() {
		if libpqKeys[k] {
			q[k] = v
		}
	}
	u.RawQuery = q.Encode()
	if db != "" {
		u.Path = "/" + db
	}
	return u.String(), password, nil
}

// Database is the URL's database name.
func (t Target) Database() string {
	u, err := url.Parse(t.URL)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

// open opens database db (the URL's when "") through lib/pq, which knows no
// sslmode=prefer: TLS first, then without.
func (t Target) open(ctx context.Context, db string) (*sql.DB, error) {
	u, err := url.Parse(t.URL)
	if err != nil {
		return nil, err
	}
	if db != "" {
		u.Path = "/" + db
	}
	q := u.Query()
	modes := []string{q.Get("sslmode")}
	if modes[0] == "" || modes[0] == "prefer" || modes[0] == "allow" {
		modes = []string{"require", "disable"}
	}
	var last error
	for _, m := range modes {
		q.Set("sslmode", m)
		u.RawQuery = q.Encode()
		d, err := sql.Open("postgres", u.String())
		if err != nil {
			return nil, err
		}
		pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = d.PingContext(pctx)
		cancel()
		if err == nil {
			d.SetMaxOpenConns(2)
			return d, nil
		}
		_ = d.Close()
		last = err
	}
	return nil, last
}

func (t Target) command(ctx context.Context, name, db string, args ...string) (*exec.Cmd, error) {
	conn, pw, err := t.parts(db)
	if err != nil {
		return nil, err
	}
	// the connection first: a dump file is a positional argument, and without GNU
	// getopt's reordering (Windows builds) an option after it is refused
	cmd := exec.CommandContext(ctx, t.bin(name), append([]string{"--dbname=" + conn}, args...)...)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+pw, "PGCONNECT_TIMEOUT=15")
	return cmd, nil
}

// Check answers whether a dump can be taken here: pg_dump runs and is of the server's
// major version or newer (an older pg_dump refuses a newer server).
func (t Target) Check(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, t.bin("pg_dump"), "--version").Output()
	if err != nil {
		return fmt.Errorf("pg_dump_missing: no pg_dump to back up the database with (%v): install the PostgreSQL client "+
			"programs of the server's version, or set MAKTABA_PG_BIN to their folder", err)
	}
	have := majorOf(string(out))
	d, err := t.open(ctx, "")
	if err != nil {
		return fmt.Errorf("db_unreachable: %w", err)
	}
	defer d.Close()
	var num int
	if err := d.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&num); err != nil {
		return fmt.Errorf("db_unreachable: %w", err)
	}
	if server := num / 10000; have > 0 && have < server {
		return fmt.Errorf("pg_dump_version: pg_dump %d cannot dump a PostgreSQL %d server", have, server)
	}
	return nil
}

var majorRe = regexp.MustCompile(`(\d+)(?:\.\d+)?`)

func majorOf(versionOut string) int {
	m := majorRe.FindStringSubmatch(versionOut)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// Dump writes a dump of the whole database to path (DumpWith).
func (t Target) Dump(ctx context.Context, path string) error {
	return t.DumpWith(ctx, path, pgbundle.DumpOptions{})
}

// DumpWith writes a directory-format dump of the database to path (pgbundle.PgDump: its
// workers, its compression, written beside and moved into place once it reads back, so a
// dump cut short or unreadable is never taken for one), its owners left out, its folder
// mode 0700.
func (t Target) DumpWith(ctx context.Context, path string, opts pgbundle.DumpOptions) error {
	conn, pw, err := t.parts("")
	if err != nil {
		return err
	}
	opts.Check = func(part string) error {
		_ = os.Chmod(part, 0o700)
		_, err := t.VerifyDump(ctx, part)
		return err
	}
	opts.PgDumpArgs = append(opts.PgDumpArgs, "--no-owner")
	if psql := t.bin("psql"); opts.Open == nil && havePSQL(psql) {
		// the vectors in binary chunks, in the dump's snapshot (pgbundle.Chunked)
		opts.Open = func(ctx context.Context) (*sql.DB, error) { return t.open(ctx, "") }
		opts.PSQL = psql
	}
	return pgbundle.PgDump(ctx, t.bin("pg_dump"), []string{"--dbname=" + conn},
		append(os.Environ(), "PGPASSWORD="+pw, "PGCONNECT_TIMEOUT=15"), path, opts)
}

// EndSessions ends the other client sessions on the database that its role may end
// (pgbundle.EndSessionsSQL): the machines' api sessions an update's migrations would
// wait for. It returns how many it ended.
func (t Target) EndSessions(ctx context.Context) (int, error) {
	d, err := t.open(ctx, "")
	if err != nil {
		return 0, err
	}
	defer d.Close()
	var n int
	return n, d.QueryRowContext(ctx, pgbundle.EndSessionsSQL("")).Scan(&n)
}

// EndBlockers ends the sessions on the database that a migration has waited on for longer
// than grace and that its role may end (pgbundle.EndBlockersSQL), and returns them.
func (t Target) EndBlockers(ctx context.Context, grace time.Duration) ([]pgbundle.EndedSession, error) {
	d, err := t.open(ctx, "")
	if err != nil {
		return nil, err
	}
	defer d.Close()
	rs, err := d.QueryContext(ctx, pgbundle.EndBlockersSQL("", grace))
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var rows []string
	for rs.Next() {
		var s string
		if err := rs.Scan(&s); err != nil {
			return nil, err
		}
		rows = append(rows, s)
	}
	return pgbundle.ParseEndedSessions(rows), rs.Err()
}

// Identity names the data the database holds: its cluster, the database and its public
// schema (their oids), and after a # the table of its migrations (a restore inside public, which
// the schema stays for, makes another). Whatever puts a dump back makes another database,
// another public schema or another such table, so the same identity is the same database that a
// restore found there. A session a
// restore cut short left (its program killed: the server goes on with what it was sent, and
// may still commit it) is ended first, and none is left when it answers. "" when
// there is none (dropped). schema is the newest migration it records (0: none; -1: it cannot
// be read now, a session holding its table).
func (t Target) Identity(ctx context.Context) (id string, schema int64, err error) {
	d, err := t.open(ctx, "")
	var pe *pq.Error
	if t.Data != "" && errors.As(err, &pe) && pe.Code == "55000" {
		// the bundled Postgres's: a restore stopped hard between closing it to new sessions and
		// putting another in its place left it closed
		if oerr := t.reopen(ctx); oerr != nil {
			return "", 0, oerr
		}
		d, err = t.open(ctx, "")
	}
	if err != nil {
		if errors.As(err, &pe) && pe.Code == "3D000" { // the database does not exist
			return "", 0, nil
		}
		return "", 0, err
	}
	defer d.Close()
	if err := endRestores(ctx, d); err != nil {
		return "", 0, err
	}
	var db, ns, table string
	if err := d.QueryRowContext(ctx, `SELECT d.oid::text, coalesce((SELECT n.oid::text FROM pg_namespace n WHERE n.nspname = 'public'), ''),
		coalesce((SELECT c.oid::text FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND c.relname = 'goose_db_version'), '')
		FROM pg_database d WHERE d.datname = current_database()`).Scan(&db, &ns, &table); err != nil {
		return "", 0, err
	}
	if ns == "" {
		return "", 0, nil
	}
	var sys string // not every operator's server lets its roles read it
	_ = d.QueryRowContext(ctx, `SELECT system_identifier::text FROM pg_control_system()`).Scan(&sys)
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if schema, err = appliedVersion(cctx, d); err != nil {
		schema = -1
	}
	return sys + "/" + db + "/" + ns + "#" + table, schema, ctx.Err()
}

// reopen lets sessions into the database again.
func (t Target) reopen(ctx context.Context) error {
	d, err := t.open(ctx, "postgres")
	if err != nil {
		return err
	}
	defer d.Close()
	if err := endRestores(ctx, d); err != nil {
		return err
	}
	_, err = d.ExecContext(ctx, `ALTER DATABASE `+pq.QuoteIdentifier(t.Database())+` ALLOW_CONNECTIONS true`)
	return err
}

// endRestores ends what restores cut short left on d's database (the bundled one's swap runs
// in postgres), and waits until it is gone.
func endRestores(ctx context.Context, d *sql.DB) error {
	for i := 0; ; i++ {
		var n int
		if err := d.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT pg_terminate_backend(pid) FROM pg_stat_activity
			WHERE application_name = $1 AND datname IN (current_database(), 'postgres') AND pid <> pg_backend_pid()) e`, pgbundle.RestoreApp).Scan(&n); err != nil || n == 0 {
			return err
		}
		if i == 100 {
			return errors.New("a session of a restore that was cut short does not end")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// SchemaVersion is the newest migration goose records as applied on the database (0:
// none).
func (t Target) SchemaVersion(ctx context.Context) (int64, error) {
	d, err := t.open(ctx, "")
	if err != nil {
		return 0, err
	}
	defer d.Close()
	var v int64
	return v, d.QueryRowContext(ctx, `SELECT coalesce(max(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&v)
}

// SchemaVersionAt is the newest migration goose had recorded as applied before at (0:
// none; its timestamps are the server's local time).
func (t Target) SchemaVersionAt(ctx context.Context, at time.Time) (int64, error) {
	d, err := t.open(ctx, "")
	if err != nil {
		return 0, err
	}
	defer d.Close()
	var v int64
	return v, d.QueryRowContext(ctx, `SELECT coalesce(max(version_id), 0) FROM goose_db_version
	    WHERE is_applied AND tstamp < to_timestamp($1)::timestamp`, float64(at.UnixMicro())/1e6).Scan(&v)
}

// GrantWorkersConnect gives the workers' group (the machines' logins) CONNECT on the
// database when it lacks it (pgbundle.GrantWorkersConnectSQL): a restore by a release
// before 2026-10-28 dropped that grant with the database.
func (t Target) GrantWorkersConnect(ctx context.Context) error {
	d, err := t.open(ctx, "")
	if err != nil {
		return err
	}
	defer d.Close()
	_, err = d.ExecContext(ctx, pgbundle.GrantWorkersConnectSQL)
	return err
}

// VerifyAside checks that path is a non-empty, readable PostgreSQL dump whose tables match
// the live database (VerifyDump) and whose counts and chunks are whole (CheckCounts,
// CheckChunks). Empty files, empty directories, and corrupt archives are rejected so they are
// never reused as protection before overwriting live data.
func (t Target) VerifyAside(ctx context.Context, path string) error {
	if !pgbundle.HasDumpHeader(path) {
		return fmt.Errorf("dump_unreadable: %s is empty or not a PostgreSQL dump", path)
	}
	if err := pgbundle.CheckCounts(path); err != nil {
		return err
	}
	list, err := t.list(ctx, path)
	if err != nil {
		return err
	}
	if err := pgbundle.CheckChunks(path, list); err != nil {
		return err
	}
	n, err := t.VerifyDump(ctx, path)
	if err != nil {
		return err
	}
	if n == 0 && len(pgbundle.ParseTOC(list)) == 0 {
		return fmt.Errorf("dump_incomplete: %s holds no objects", path)
	}
	return nil
}

// VerifyDump checks a dump reads back: pg_restore --list parses it and names every
// table the database has now in public (a dump of an empty database lists none). It
// returns how many tables the dump holds.
func (t Target) VerifyDump(ctx context.Context, path string) (int, error) {
	out, err := exec.CommandContext(ctx, t.bin("pg_restore"), "--list", path).Output()
	if err != nil {
		return 0, fmt.Errorf("dump_unreadable: pg_restore --list %s: %w", path, err)
	}
	inDump := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		// "1234; 1259 16390 TABLE public videos maktaba"
		f := strings.Fields(sc.Text())
		for i := 0; i+2 < len(f); i++ {
			if f[i] == "TABLE" && f[i+1] == "public" {
				inDump[f[i+2]] = true
				break
			}
		}
	}
	d, err := t.open(ctx, "")
	if err != nil {
		return len(inDump), nil // the database is not reachable to compare: the dump parsed
	}
	defer d.Close()
	rows, err := d.QueryContext(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		return len(inDump), nil
	}
	defer rows.Close()
	var missing []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && !inDump[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return len(inDump), fmt.Errorf("dump_incomplete: %s lacks %d table(s): %s", path, len(missing), strings.Join(missing, ", "))
	}
	return len(inDump), nil
}

// Size is the database's size in bytes.
func (t Target) Size(ctx context.Context) (int64, error) {
	d, err := t.open(ctx, "")
	if err != nil {
		return 0, err
	}
	defer d.Close()
	var n int64
	err = d.QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&n)
	return n, err
}

// RehearsePrefix starts every scratch database's name (dropped at the next start when
// a crash left one).
const RehearsePrefix = "maktaba_rehearse_"

// Result is what a rehearsal measured.
type Result struct {
	Skipped    string
	DBBytes    int64
	Pending    int
	MigrateSec float64
}

// Rehearse dumps the database, restores the dump into a scratch database, runs api's
// migrations (dir) on it and drops it: a migration that fails on the real data fails
// here, before anything is installed. Skipped (Result.Skipped) above maxGB, or when the
// role cannot create databases, or the database's extensions in another (a restore inside
// public keeps those that are there: a scratch database has none).
func (t Target) Rehearse(ctx context.Context, api, dir string, maxGB int, log io.Writer) (Result, error) {
	return t.rehearse(ctx, api, dir, int64(maxGB)<<30, log)
}

func (t Target) rehearse(ctx context.Context, api, dir string, maxBytes int64, log io.Writer) (Result, error) {
	var r Result
	size, err := t.Size(ctx)
	if err != nil {
		return r, fmt.Errorf("db_unreachable: %w", err)
	}
	r.DBBytes = size
	if maxBytes > 0 && size > maxBytes {
		r.Skipped = fmt.Sprintf("the database is %d MB, above rehearse_max_db_gb (%d MB)", size>>20, maxBytes>>20)
		return r, nil
	}
	d, err := t.open(ctx, "")
	if err != nil {
		return r, fmt.Errorf("db_unreachable: %w", err)
	}
	defer d.Close()
	var can bool
	if err := d.QueryRowContext(ctx, `SELECT rolcreatedb OR rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&can); err != nil {
		return r, fmt.Errorf("db_unreachable: %w", err)
	}
	if !can {
		r.Skipped = "the database role may not create databases"
		return r, nil
	}
	applied, err := appliedVersion(ctx, d)
	if err != nil {
		return r, err
	}
	r.Pending = pendingIn(dir, applied)
	if r.Pending == 0 {
		r.Skipped = "the release adds no migrations"
		return r, nil
	}
	scratch := RehearsePrefix + randHex(4)
	if _, err := d.ExecContext(ctx, `CREATE DATABASE `+scratch+` TEMPLATE template0 ENCODING 'UTF8'`); err != nil {
		return r, fmt.Errorf("rehearsal_scratch: %w", err)
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = d.ExecContext(dctx, `DROP DATABASE IF EXISTS `+scratch+` WITH (FORCE)`)
	}()
	if ext, err := t.mayNotMakeIn(ctx, d, scratch); err != nil {
		return r, fmt.Errorf("rehearsal_scratch: %w", err)
	} else if ext != "" {
		r.Skipped = "the database role may not make the extension " + ext + " in a scratch database (it needs a superuser)"
		return r, nil
	}
	tmp, err := os.MkdirTemp(t.Backups, ".rehearse-")
	if err != nil {
		if err := os.MkdirAll(t.Backups, 0o700); err != nil {
			return r, err
		}
		if tmp, err = os.MkdirTemp(t.Backups, ".rehearse-"); err != nil {
			return r, err
		}
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	dump := filepath.Join(tmp, "rehearse.dump")
	if err := t.Dump(ctx, dump); err != nil {
		return r, fmt.Errorf("rehearsal_dump: %w", err)
	}
	if err := t.restoreInto(ctx, scratch, dump); err != nil {
		return r, fmt.Errorf("rehearsal_restore: %w", err)
	}
	scratchURL, err := t.urlFor(scratch)
	if err != nil {
		return r, err
	}
	mctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	// the URL in the environment without its password, never on the command line
	cmd := exec.CommandContext(mctx, api, "migrate", "--dir", dir, "--timeout", "30m", "up")
	// the scratch copy has no other user, and the update's own migrations run with the
	// library stopped: both build their indexes plainly (api/migrate_offline.go), so the
	// rehearsal runs what the update will
	env, err := dsn.ProcessEnv(append(withoutNodeMode(os.Environ()), "DATABASE_URL="+scratchURL,
		"MAKTABA_MIGRATE_OFFLINE=1"), config.KeysDir())
	if err != nil {
		return r, err
	}
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(&out, orDiscard(log))
	cmd.Stderr = cmd.Stdout
	start := time.Now()
	err = cmd.Run()
	r.MigrateSec = time.Since(start).Seconds()
	if err != nil {
		return r, fmt.Errorf("rehearsal_failed: the release's migrations failed on a copy of this database: %s", tail(out.Bytes()))
	}
	return r, nil
}

// restoreInto restores dump into the empty database db of the same server, in parallel:
// a dump with chunked tables (pgbundle.Chunked) in three steps, its tables and data, the
// chunks, then its indexes and constraints.
func (t Target) restoreInto(ctx context.Context, db, dump string) error {
	jobs := pgbundle.DumpJobs()
	restore := func(sections ...string) error {
		args := append([]string{"--no-owner", "--exit-on-error", "--jobs=" + strconv.Itoa(jobs)}, sections...)
		cmd, err := t.command(ctx, "pg_restore", db, append(args, dump)...)
		if err != nil {
			return err
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("pg_restore into %s: %v: %s", db, err, tail(out))
		}
		return nil
	}
	chunks := pgbundle.Chunks(dump)
	if len(chunks) == 0 {
		return restore()
	}
	if err := pgbundle.CheckChunks(dump, ""); err != nil {
		return err
	}
	if err := restore("--section=pre-data", "--section=data"); err != nil {
		return err
	}
	conn, pw, err := t.parts(db)
	if err != nil {
		return err
	}
	if err := pgbundle.LoadChunks(ctx, t.bin("psql"), []string{"--dbname=" + conn},
		append(os.Environ(), "PGPASSWORD="+pw, "PGCONNECT_TIMEOUT=15"), chunks, jobs); err != nil {
		return err
	}
	return restore("--section=post-data")
}

// urlFor is the URL of another database on the same server, for lib/pq (maktaba-api).
func (t Target) urlFor(db string) (string, error) {
	u, err := url.Parse(t.URL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + db
	return u.String(), nil
}

// DropScratch drops scratch databases a rehearsal left (a crash during one).
func (t Target) DropScratch(ctx context.Context) error {
	d, err := t.open(ctx, "")
	if err != nil {
		return err
	}
	defer d.Close()
	rows, err := d.QueryContext(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'maktaba\_rehearse\_%'`)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && strings.HasPrefix(n, RehearsePrefix) && validIdent(n) {
			names = append(names, n)
		}
	}
	_ = rows.Close()
	for _, n := range names {
		if _, err := d.ExecContext(ctx, `DROP DATABASE IF EXISTS `+n+` WITH (FORCE)`); err != nil {
			return err
		}
	}
	return nil
}

var identRe = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func validIdent(s string) bool { return identRe.MatchString(s) }

// Restore puts a dump back into the database, in place: the machines' copy slots are
// dropped (each machine copies the restored database anew), the other sessions on it
// are ended, and the public schema made afresh with the dump restored in it, all in one
// transaction (psql over pg_restore's script): a restore that fails leaves the database
// as it was, never empty. Not its publications, which outlive the schema and are brought
// up to date with the restored tables at a machine's next grant. Without psql beside
// pg_restore nothing is done at all: such a restore cannot be made whole or nothing, so
// it is refused (nothing changed). For a Postgres the home does not bundle (the bundled
// one is restored by pgbundle.RestoreRunning).
func (t Target) Restore(ctx context.Context, dump string, log io.Writer) error {
	return t.RestoreWith(ctx, dump, nil, log)
}

// RestoreWith is Restore, with before called before each step that destroys or replaces
// the database (pgbundle.RestoreOptions.Before; nil: nothing to tell).
func (t Target) RestoreWith(ctx context.Context, dump string, before func(step string) error, log io.Writer) error {
	tell := func(step string) error {
		if before == nil {
			return nil
		}
		return before(step)
	}
	if _, err := os.Stat(dump); err != nil {
		return err
	}
	// a damaged dump is refused before anything is touched
	list, err := t.list(ctx, dump)
	if err == nil {
		err = pgbundle.CheckCounts(dump)
	}
	if err == nil {
		err = pgbundle.CheckChunks(dump, list)
	}
	if err != nil {
		return fmt.Errorf("restore %s (nothing changed): %w", dump, err)
	}
	d, err := t.open(ctx, "")
	if err != nil {
		return err
	}
	defer d.Close()
	psql := havePSQL(t.bin("psql"))
	how, err := t.howTo(ctx, d, dump, list, psql)
	if err != nil {
		return fmt.Errorf("restore %s (nothing changed): %w", dump, err)
	}
	if how.inside {
		outer := tell
		tell = func(step string) error {
			if step == pgbundle.StepReplaced {
				step = pgbundle.StepInside
			}
			return outer(step)
		}
	}
	only, err := listFile(how.list, list)
	if err != nil {
		return fmt.Errorf("restore %s (nothing changed): %w", dump, err)
	}
	if only != "" {
		defer func() { _ = os.Remove(only) }()
	}
	if _, err := d.ExecContext(ctx, pgbundle.DropSlotsSQL("")); err != nil {
		// a database user without REPLICATION made no copies; one whose sessions it may
		// not end keeps the copies streaming what is about to go
		_, _ = fmt.Fprintf(orDiscard(log), "[database] the machines' copies keep their slots (%v): drop them "+
			"(pg_drop_replication_slot) so each machine copies the restored database anew\n", err)
	}
	if _, err := d.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname = current_database() AND pid <> pg_backend_pid()`); err != nil {
		// one it may not end (a superuser's) stops that at once: those it may, then
		_, _ = d.ExecContext(ctx, pgbundle.EndSessionsSQL(""))
	}
	if err := t.restoreAtomic(ctx, dump, list, how.empty, only, tell, log); err != nil {
		return err
	}
	_, _ = d.ExecContext(ctx, `ANALYZE`)
	return t.verifyRestore(ctx, d, dump, how.list)
}

// childWaitDelay is how long a child's pipes may stay open after the child itself ended
// (exec.Cmd's WaitDelay): psql's own, and pg_restore's, whose stdout a grandchild may hold
// (tests shorten it).
var childWaitDelay = 10 * time.Second

// psqlLookEvery is how often a restore looks at its psql's session (tests look more often),
// and psqlLooks how many looks in a row that say its transaction is over end it.
var psqlLookEvery = time.Second

const psqlLooks = 5

// psqlSession is the session psql's script says it runs in: a line of word, the backend's pid,
// and when its server started. It is a writer (psql's own output).
type psqlSession struct {
	word string

	mu          sync.Mutex
	last        []byte
	pid, server string
}

func (s *psqlSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pid != "" {
		return len(p), nil
	}
	s.last = append(s.last, p...)
	if i := bytes.Index(s.last, []byte(s.word)); i >= 0 {
		if line, _, whole := bytes.Cut(s.last[i:], []byte("\n")); whole {
			if f := strings.Fields(string(line)); len(f) == 3 {
				s.pid, s.server = f[1], f[2]
			}
			s.last = nil
		}
	} else if n := len(s.word); len(s.last) > n {
		s.last = append([]byte(nil), s.last[len(s.last)-n:]...)
	}
	return len(p), nil
}

func (s *psqlSession) said() (pid, server string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pid, s.server
}

// watchSession calls stuck, once, when the session at says is over while psql still runs: its
// transaction failed, or it is there no more on the server it ran on, at psqlLooks looks in a
// row. What cannot be seen (no answer, another server, a state its role may not see) says
// nothing.
func (t Target) watchSession(ctx context.Context, at *psqlSession, stuck func(state string)) {
	tick := time.NewTicker(psqlLookEvery)
	defer tick.Stop()
	for over := 0; ; {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		pid, server := at.said()
		if pid == "" {
			continue
		}
		state, there, known := t.session(ctx, pid, server)
		if !known || (there && state != "idle in transaction (aborted)") {
			over = 0
			continue
		}
		if !there {
			state = "gone"
		}
		if over++; over >= psqlLooks {
			stuck(state)
			return
		}
	}
}

// session is what the backend pid of the server started at server does (pg_stat_activity's
// state), and whether it is there. known false: it cannot be told (no answer, or the look
// reached another server).
func (t Target) session(ctx context.Context, pid, server string) (state string, there, known bool) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d, err := t.open(cctx, "")
	if err != nil {
		return "", false, false
	}
	defer d.Close()
	var on string
	var n int
	if err := d.QueryRowContext(cctx, `SELECT extract(epoch FROM pg_postmaster_start_time())::text,
		(SELECT count(*) FROM pg_stat_activity WHERE pid = $1::int),
		coalesce((SELECT state FROM pg_stat_activity WHERE pid = $1::int), '')`, pid).Scan(&on, &n, &state); err != nil {
		return "", false, false
	}
	return state, n > 0, on == server
}

// signal is a writer that closes seen once word was written to it.
type signal struct {
	word string
	seen chan struct{}
	last []byte
	done bool
}

func newSignal(word string) *signal { return &signal{word: word, seen: make(chan struct{})} }

func (s *signal) Write(p []byte) (int, error) {
	if s.done {
		return len(p), nil
	}
	s.last = append(s.last, p...)
	if bytes.Contains(s.last, []byte(s.word)) {
		s.done, s.last = true, nil
		close(s.seen)
	} else if n := len(s.word); len(s.last) > n {
		s.last = append([]byte(nil), s.last[len(s.last)-n:]...)
	}
	return len(p), nil
}

// verifyRestore checks the database put back against the dump, by the list it was restored by
// (pgbundle.VerifyRestore: every object, grant and default privilege, every table's rows): a
// restore is recorded done only when it is whole, and one that is not says what it lacks.
func (t Target) verifyRestore(ctx context.Context, d *sql.DB, dump, toc string) error {
	query := func(ctx context.Context, q string) ([]string, error) {
		rows, err := d.QueryContext(ctx, q)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s sql.NullString
			if err := rows.Scan(&s); err != nil {
				return nil, err
			}
			out = append(out, s.String)
		}
		return out, rows.Err()
	}
	if err := pgbundle.VerifyRestore(ctx, dump, toc, query); err != nil {
		return fmt.Errorf("restore %s: %w", dump, err)
	}
	return nil
}

// list is a dump's table of contents (pg_restore -l).
func (t Target) list(ctx context.Context, dump string) (string, error) {
	cmd := exec.CommandContext(ctx, t.bin("pg_restore"), "-l", dump)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pg_restore -l %s: %v: %s", dump, err, tail(stderr.Bytes()))
	}
	return string(out), nil
}

// havePSQL reports whether psql runs.
var havePSQL = func(psql string) bool {
	return exec.Command(psql, "--version").Run() == nil
}

// restoreAtomic empties the public schema (empty: howTo's) and restores the dump in it in one
// transaction: pg_restore's script (no publications, no owners) streams into psql, which
// runs it after BEGIN and that, stopping at the first error (the
// transaction rolls back: the database stays as it was). Nothing is written to disk (a plain
// script is several times the dump's size, which the space check does not count). COMMIT is
// the script's own last line, written once psql ran all of it (tell first): an end of
// psql's input before that (a pg_restore that fails part way, this process killed) commits
// nothing. Until 2026-11-25 it was psql's --single-transaction, which commits at the end of
// its input, wherever that is: a server killed in the middle of a restore left a part of it
// committed. A psql that stops first (an
// SQL error, ON_ERROR_STOP) stops pg_restore too (it would write into a pipe nobody reads
// for ever), and its own words are the error. The dump is streamed
// in three parts: its tables and their rows (copyScan reads them too: one whose rows are cut
// short goes no further), a \copy of each chunk of its chunked tables (pgbundle.Chunked;
// CheckChunks refused a damaged one before), with every table's rows counted, and its indexes
// and constraints.
func (t Target) restoreAtomic(ctx context.Context, dump, list, empty, only string, tell func(step string) error, log io.Writer) error {
	pctx, kill := context.WithCancel(ctx)
	defer kill()
	counts, err := pgbundle.CountsScript(dump, list)
	if err != nil {
		return fmt.Errorf("restore %s (nothing changed): %w", dump, err)
	}
	tables := 0
	for _, e := range pgbundle.ParseTOC(list) {
		if e.Desc == "TABLE DATA" {
			tables++
		}
	}
	// one run of the tables and their rows: pg_restore gives their grants after them both
	const dataPart, chunkPart, postPart = 0, 1, 2
	cmd, err := t.command(pctx, "psql", "", "--no-psqlrc", "--quiet", "--set=ON_ERROR_STOP=1", "--file=-")
	if err != nil {
		return err
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var out bytes.Buffer // read once psql ended
	word := "maktaba-restore-" + randHex(8)
	ran, at := newSignal(word+"-ran"), &psqlSession{word: word + "-session"}
	cmd.WaitDelay = childWaitDelay // its output pipe, should a child outlive it
	cmd.Stdout = io.MultiWriter(&out, orDiscard(log), ran, at)
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("psql: %w", err)
	}
	var werr error
	ended := make(chan struct{})
	go func() {
		werr = cmd.Wait()
		close(ended)
	}()
	// a psql whose transaction failed and that neither ends nor reads would be waited for for
	// ever, writing its script or after it
	var stuck atomic.Pointer[string]
	go t.watchSession(pctx, at, func(state string) {
		stuck.Store(&state)
		kill()
	})
	// stopped ends psql (the server rolls its transaction back) and says why, why when psql
	// itself says nothing.
	stopped := func(why error) error {
		kill()
		<-ended
		if state := stuck.Load(); state != nil {
			return fmt.Errorf("restore %s (nothing changed: one transaction): psql failed and did not end (its session: %s): %s",
				dump, *state, tail(out.Bytes()))
		}
		if werr != nil {
			why = werr
		}
		return fmt.Errorf("restore %s (nothing changed: one transaction): psql stopped: %v: %s", dump, why, tail(out.Bytes()))
	}
	write := func(sql string) error {
		_, err := io.WriteString(in, sql)
		return err
	}
	// its session, for the watch; its name, for Identity (the connection's own name, from its
	// URL or a pooler, is another)
	if err := write("BEGIN;\nSET LOCAL application_name = '" + pgbundle.RestoreApp + "';\n" +
		"SELECT pg_catalog.pg_backend_pid() AS mk_pid, extract(epoch FROM pg_catalog.pg_postmaster_start_time()) AS mk_server \\gset\n" +
		"\\warn " + at.word + " :mk_pid :mk_server\n" + empty); err != nil {
		return stopped(err)
	}
	for part := dataPart; part <= postPart; part++ {
		args := []string{"--no-owner", "--no-publications", "--section=post-data", "--file=-"}
		switch part {
		case chunkPart:
			// the chunks, then every table's rows counted, before the indexes are built
			if err := write(pgbundle.CopyScript(pgbundle.Chunks(dump)) + counts); err != nil {
				return stopped(err)
			}
			continue
		case dataPart:
			args = []string{"--no-owner", "--no-publications", "--section=pre-data", "--section=data", "--file=-"}
		}
		gen := exec.CommandContext(pctx, t.bin("pg_restore"), append(append(args, useList(only)...), dump)...)
		// Own the stdout pipe ourselves instead of gen.StdoutPipe(): calling gen.Wait()
		// concurrently with reading StdoutPipe() violates os/exec's contract and closes the
		// pipe before unread output drains when psql backpressures. With an owned os.Pipe,
		// gen.Wait() never touches pr; once pg_restore exits, post-exit reads from pr are
		// bounded by childWaitDelay so a grandchild inheriting stdout cannot hang forever,
		// while slow psql writes still drain every buffered byte without truncation.
		pr, pw, err := os.Pipe()
		if err != nil {
			return stopped(err)
		}
		var genErr bytes.Buffer
		gen.Stdout = pw
		gen.Stderr = &genErr
		gen.WaitDelay = childWaitDelay // bounds stderr if a grandchild holds it
		if err := gen.Start(); err != nil {
			_ = pr.Close()
			_ = pw.Close()
			return stopped(fmt.Errorf("pg_restore: %w", err))
		}
		_ = pw.Close() // only pg_restore (and any inheriting child) holds the write end now
		var scan copyScan
		var genDone atomic.Bool
		copyFinished := make(chan struct{})
		copyDone := make(chan error, 1)
		dst := io.MultiWriter(in, &scan)
		go func() {
			defer close(copyFinished)
			buf := make([]byte, 32<<10)
			waitLeft := childWaitDelay
			for {
				if genDone.Load() {
					if waitLeft <= 0 {
						copyDone <- fmt.Errorf("pg_restore stdout stayed open after it exited: %w", os.ErrDeadlineExceeded)
						return
					}
					_ = pr.SetReadDeadline(time.Now().Add(waitLeft))
				}
				t0 := time.Now()
				n, rerr := pr.Read(buf)
				if genDone.Load() {
					waitLeft -= time.Since(t0)
				}
				if n > 0 {
					if _, werr := dst.Write(buf[:n]); werr != nil {
						copyDone <- werr
						return
					}
				}
				if errors.Is(rerr, io.EOF) {
					copyDone <- nil
					return
				}
				if rerr != nil {
					copyDone <- rerr
					return
				}
			}
		}()
		waitDone := make(chan error, 1)
		go func() {
			werr := gen.Wait()
			genDone.Store(true)
			if derr := pr.SetReadDeadline(time.Now().Add(childWaitDelay)); derr != nil {
				// Fallback for platforms whose os.Pipe does not support read deadlines (Windows):
				// close pr after childWaitDelay if the copier has not already finished draining.
				t := time.AfterFunc(childWaitDelay, func() { _ = pr.Close() })
				go func() {
					<-copyFinished
					t.Stop()
				}()
			}
			waitDone <- werr
		}()
		cerr := <-copyDone
		_ = pr.Close()
		if cerr != nil {
			err := stopped(cerr)
			<-waitDone // psql's end (killed by the same context) ends pg_restore too
			return err
		}
		if werr := <-waitDone; werr != nil {
			// the script is not whole: psql ends before its input does, with no commit
			kill()
			<-ended
			return fmt.Errorf("pg_restore %s (nothing changed: one transaction): %v: %s", dump, werr, tail(genErr.Bytes()))
		}
		if part == dataPart {
			if err := scan.whole(tables); err != nil {
				// psql takes what comes next for rows: nothing more goes to it
				kill()
				<-ended
				return fmt.Errorf("restore %s (nothing changed: one transaction): %w", dump, err)
			}
		}
	}
	// the whole script: once psql ran all of it (it says so), the commit, its last line
	if err := write("\n\\warn " + ran.word + "\n"); err != nil {
		return stopped(err)
	}
	select {
	case <-ran.seen:
	case <-ended:
		return stopped(nil)
	}
	if err := tell(pgbundle.StepReplaced); err != nil {
		kill()
		<-ended
		return fmt.Errorf("restore %s (nothing changed: one transaction): %w", dump, err)
	}
	if n, err := io.WriteString(in, "COMMIT;\n"); err != nil {
		if n == 0 {
			return stopped(err) // COMMIT was never written to psql: nothing committed
		}
		kill()
		<-ended
		return fmt.Errorf("restore %s (one transaction, not known to be committed): %v: %s", dump, err, tail(out.Bytes()))
	}
	_ = in.Close()
	if <-ended; werr != nil {
		return fmt.Errorf("restore %s (one transaction, not known to be committed): %v: %s", dump, werr, tail(out.Bytes()))
	}
	return nil
}

func appliedVersion(ctx context.Context, d *sql.DB) (int64, error) {
	var has bool
	if err := d.QueryRowContext(ctx, `SELECT to_regclass('goose_db_version') IS NOT NULL`).Scan(&has); err != nil {
		return 0, err
	}
	if !has {
		return 0, nil
	}
	var v int64
	err := d.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&v)
	return v, err
}

// Applied is the newest migration the database holds (goose's table; 0 before the
// first).
func (t Target) Applied(ctx context.Context) (int64, error) {
	d, err := t.open(ctx, "")
	if err != nil {
		return 0, err
	}
	defer d.Close()
	return appliedVersion(ctx, d)
}

// Pending counts the release's migrations (the folder dir) the database does not hold:
// what its start would run. ok is false when that cannot be told (no folder, the database
// not reachable).
func (t Target) Pending(ctx context.Context, dir string) (n int, ok bool) {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return 0, false
	}
	applied, err := t.Applied(ctx)
	if err != nil {
		return 0, false
	}
	return pendingIn(dir, applied), true
}

// pendingIn counts dir's Postgres migrations above applied.
func pendingIn(dir string, applied int64) int {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".sqlite.sql") {
			continue
		}
		slot, _, ok := strings.Cut(name, "_")
		if !ok {
			continue
		}
		if v, err := strconv.ParseInt(slot, 10, 64); err == nil && v > applied {
			n++
		}
	}
	return n
}

func withoutNodeMode(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "MAKTABA_NODE_MODE", "DATABASE_URL", "MAKTABA_DATABASE_URL":
			continue
		}
		out = append(out, kv)
	}
	return out
}

func tail(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	return strings.Join(lines, " | ")
}

func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ErrNoDump: nothing to put back.
var ErrNoDump = errors.New("no dump")
