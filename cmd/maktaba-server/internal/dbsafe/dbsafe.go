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
}

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
			"programs of the server's version, or set [postgres] bin_dir", err)
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

// Dump writes a custom-format dump of the database to path (beside, then renamed: a
// dump cut short is never taken for one), mode 0600, and checks it reads back.
func (t Target) Dump(ctx context.Context, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	part := path + ".partial"
	_ = os.Remove(part)
	cmd, err := t.command(ctx, "pg_dump", "", "--format=custom", "--no-owner", "--file="+part)
	if err != nil {
		return err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("pg_dump: %w: %s", err, strings.TrimSpace(string(out)))
	}
	_ = os.Chmod(part, 0o600)
	if _, err := t.VerifyDump(ctx, part); err != nil {
		_ = os.Remove(part)
		return err
	}
	return os.Rename(part, path)
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
	restore, err := t.command(ctx, "pg_restore", scratch, "--no-owner", "--exit-on-error", dump)
	if err != nil {
		return r, err
	}
	if out, err := restore.CombinedOutput(); err != nil {
		return r, fmt.Errorf("rehearsal_restore: pg_restore into %s: %v: %s", scratch, err, tail(out))
	}
	scratchURL, err := t.urlFor(scratch)
	if err != nil {
		return r, err
	}
	mctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(mctx, api, "migrate", "--dir", dir, "--dsn", scratchURL, "--timeout", "30m", "up")
	cmd.Env = withoutNodeMode(os.Environ())
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
// are ended, the public schema is made afresh, and the dump restored (--exit-on-error,
// one transaction; not its publications, which outlive the schema and are brought up to
// date with the restored tables at a machine's next grant). For a Postgres the home does
// not bundle (the bundled one is restored by pgbundle.RestoreRunning).
func (t Target) Restore(ctx context.Context, dump string, log io.Writer) error {
	if _, err := os.Stat(dump); err != nil {
		return err
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
