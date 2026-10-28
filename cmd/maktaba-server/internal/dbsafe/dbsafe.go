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
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dsn"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	_ "github.com/lib/pq" // the postgres driver
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
// role cannot create databases.
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
	scratch := RehearsePrefix + randHex(4)
	if _, err := d.ExecContext(ctx, `CREATE DATABASE `+scratch+` TEMPLATE template0 ENCODING 'UTF8'`); err != nil {
		return r, fmt.Errorf("rehearsal_scratch: %w", err)
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = d.ExecContext(dctx, `DROP DATABASE IF EXISTS `+scratch+` WITH (FORCE)`)
	}()
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
	env, err := dsn.ProcessEnv(append(withoutNodeMode(os.Environ()), "DATABASE_URL="+scratchURL), config.KeysDir())
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
// up to date with the restored tables at a machine's next grant. Without psql, the schema
// is made afresh and the dump restored in a transaction of its own (as before). For a
// Postgres the home does not bundle (the bundled one is restored by
// pgbundle.RestoreRunning).
func (t Target) Restore(ctx context.Context, dump string, log io.Writer) error {
	if _, err := os.Stat(dump); err != nil {
		return err
	}
	// a damaged dump is refused before the database is touched
	if err := pgbundle.CheckCounts(dump); err != nil {
		return fmt.Errorf("restore %s (nothing changed): %w", dump, err)
	}
	d, err := t.open(ctx, "")
	if err != nil {
		return err
	}
	defer d.Close()
	if _, err := d.ExecContext(ctx, pgbundle.DropSlotsSQL("")); err != nil {
		// a database user without REPLICATION made no copies; one whose sessions it may
		// not end keeps the copies streaming what is about to go
		_, _ = fmt.Fprintf(orDiscard(log), "[database] the machines' copies keep their slots (%v): drop them "+
			"(pg_drop_replication_slot) so each machine copies the restored database anew\n", err)
	}
	_, _ = d.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname = current_database() AND pid <> pg_backend_pid()`)
	if havePSQL(t.bin("psql")) {
		if err := t.restoreAtomic(ctx, dump, log); err != nil {
			return err
		}
		_, _ = d.ExecContext(ctx, `ANALYZE`)
		return t.verifyRestore(ctx, d, dump)
	}
	if len(pgbundle.Chunks(dump)) > 0 {
		return fmt.Errorf("restore %s: its vectors are binary chunks that psql loads, and there is no psql beside "+
			"pg_restore (install the PostgreSQL client programs, or set MAKTABA_PG_BIN): nothing was changed", dump)
	}
	if _, err := d.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		return fmt.Errorf("make the public schema afresh: %w", err)
	}
	cmd, err := t.command(ctx, "pg_restore", "", "--no-owner", "--no-publications", "--exit-on-error", "--single-transaction", dump)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(&out, orDiscard(log))
	cmd.Stderr = cmd.Stdout
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore %s: %v: %s", dump, err, tail(out.Bytes()))
	}
	_, _ = d.ExecContext(ctx, `ANALYZE`)
	return t.verifyRestore(ctx, d, dump)
}

// verifyRestore checks the database put back against the dump (pgbundle.VerifyRestore:
// every object, grant and default privilege, every table's rows): a restore is recorded
// done only when it is whole, and one that is not says what it lacks.
func (t Target) verifyRestore(ctx context.Context, d *sql.DB, dump string) error {
	cmd := exec.CommandContext(ctx, t.bin("pg_restore"), "-l", dump)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	toc, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("pg_restore -l %s: %v: %s", dump, err, tail(stderr.Bytes()))
	}
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
	if err := pgbundle.VerifyRestore(ctx, dump, string(toc), query); err != nil {
		return fmt.Errorf("restore %s: %w", dump, err)
	}
	return nil
}

// havePSQL reports whether psql runs.
var havePSQL = func(psql string) bool {
	return exec.Command(psql, "--version").Run() == nil
}

// restoreAtomic makes the public schema afresh and restores the dump in it in one
// transaction: pg_restore's script (no publications, no owners) streams into psql, which
// runs it after the schema's drop and creation with --single-transaction, stopping at the
// first error (the transaction rolls back: the database stays as it was). Nothing is
// written to disk (a plain script is several times the dump's size, which the space check
// does not count). psql commits only at the end of its input: its input is closed once
// pg_restore wrote the whole script; a pg_restore that fails part way (a cut dump) ends
// psql before that, so the server rolls the transaction back. A psql that stops first (an
// SQL error, ON_ERROR_STOP) stops pg_restore too (it would write into a pipe nobody reads
// for ever), and its own words are the error. A dump with chunked tables (pgbundle.Chunked)
// is streamed in three parts: its tables and their data, a \copy of each chunk, then its
// indexes and constraints.
func (t Target) restoreAtomic(ctx context.Context, dump string, log io.Writer) error {
	pctx, kill := context.WithCancel(ctx)
	defer kill()
	parts := [][]string{nil} // pg_restore's sections (nil: all of it); a "" marks the chunks
	if chunks := pgbundle.Chunks(dump); len(chunks) > 0 {
		parts = [][]string{{"--section=pre-data", "--section=data"}, {""}, {"--section=post-data"}}
	}
	cmd, err := t.command(pctx, "psql", "", "--no-psqlrc", "--quiet", "--set=ON_ERROR_STOP=1", "--single-transaction",
		"--command=DROP SCHEMA public CASCADE; CREATE SCHEMA public;", "--file=-")
	if err != nil {
		return err
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(&out, orDiscard(log))
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("psql: %w", err)
	}
	stopped := func(copyErr error) error {
		// psql stopped reading (its own error says why, its transaction rolled back): say
		// what psql said
		kill()
		werr := cmd.Wait()
		if werr == nil {
			werr = copyErr
		}
		return fmt.Errorf("restore %s (nothing changed: one transaction): psql stopped: %v: %s", dump, werr, tail(out.Bytes()))
	}
	for _, sections := range parts {
		if len(sections) == 1 && sections[0] == "" {
			if _, err := io.WriteString(in, pgbundle.CopyScript(pgbundle.Chunks(dump))); err != nil {
				return stopped(err)
			}
			continue
		}
		args := append(append([]string{"--no-owner", "--no-publications"}, sections...), "--file=-", dump)
		gen := exec.CommandContext(pctx, t.bin("pg_restore"), args...)
		script, err := gen.StdoutPipe()
		if err != nil {
			kill()
			_ = cmd.Wait()
			return err
		}
		var genErr bytes.Buffer
		gen.Stderr = &genErr
		if err := gen.Start(); err != nil {
			kill()
			_ = cmd.Wait()
			return fmt.Errorf("pg_restore: %w", err)
		}
		if _, copyErr := io.Copy(in, script); copyErr != nil {
			// pg_restore would wait on the pipe for ever: stopped with psql
			err := stopped(copyErr)
			_ = gen.Wait()
			return err
		}
		if err := gen.Wait(); err != nil {
			// the script is not whole: psql ends before its input does, with no commit
			kill()
			_ = cmd.Wait()
			return fmt.Errorf("pg_restore %s (nothing changed: one transaction): %v: %s", dump, err, tail(genErr.Bytes()))
		}
	}
	_ = in.Close() // the whole script: psql commits at its end
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("restore %s (nothing changed: one transaction): %v: %s", dump, err, tail(out.Bytes()))
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
