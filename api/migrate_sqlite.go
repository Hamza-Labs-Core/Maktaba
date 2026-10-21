package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver ("sqlite")
)

// SQLite migration path.
//
// goose cannot drive the *.sqlite.sql twins on stock SQLite: twelve of them use
// `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`, which SQLite has no syntax for, and
// the 0004 twin rebuilds `videos` with a `last_seen_at` column that only slot
// 0007 adds. This applier reads the same files, in slot order, and keeps the
// same bookkeeping table goose uses (`goose_db_version`), but rewrites those
// two constructs while it applies them. Old migrations are never edited.

const sqliteVersionTable = "goose_db_version"

// sqliteLegacyPrelude holds statements run before a legacy twin so that it
// applies on a fresh database. Keep this list to real defects of old twins.
var sqliteLegacyPrelude = map[int64][]string{
	// 0004 rebuilds videos and copies last_seen_at and deleted_at, which slot 0007 adds later.
	4: {
		"ALTER TABLE videos ADD COLUMN IF NOT EXISTS last_seen_at TEXT",
		"ALTER TABLE videos ADD COLUMN IF NOT EXISTS deleted_at TEXT",
	},
}

var addColumnIfNotExists = regexp.MustCompile(
	`(?is)^\s*ALTER\s+TABLE\s+("?\w+"?)\s+ADD\s+COLUMN\s+IF\s+NOT\s+EXISTS\s+("?\w+"?)\s+(.*?);?\s*$`)

var pragmaForeignKeys = regexp.MustCompile(`(?im)^\s*PRAGMA\s+foreign_keys`)

// isSQLiteDSN reports whether a DSN selects the SQLite backend.
func isSQLiteDSN(dsn string) bool {
	l := strings.ToLower(strings.TrimSpace(dsn))
	return strings.HasPrefix(l, "sqlite:") || strings.HasPrefix(l, "sqlite3:") || strings.HasPrefix(l, "file:")
}

// sqliteDriverPath turns a sqlite:// DSN into what modernc.org/sqlite opens.
func sqliteDriverPath(dsn string) string {
	d := strings.TrimSpace(dsn)
	for _, p := range []string{"sqlite3://", "sqlite://", "sqlite3:", "sqlite:"} {
		if len(d) >= len(p) && strings.EqualFold(d[:len(p)], p) {
			return d[len(p):]
		}
	}
	return d
}

type sqliteMigration struct {
	version int64
	name    string
	path    string
}

func listSQLiteMigrations(dir string) ([]sqliteMigration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []sqliteMigration
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sqlite.sql") || len(name) < 5 {
			continue
		}
		v, err := strconv.ParseInt(name[:4], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("sqlite migration %q: slot is not numeric", name)
		}
		out = append(out, sqliteMigration{version: v, name: name, path: filepath.Join(dir, name)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// upStatements returns the statements of the `-- +goose Up` section, using the
// same rules goose does: a StatementBegin/End block is one statement, otherwise
// a statement ends at a line that ends with `;`.
func upStatements(raw string) []string {
	var stmts, buf []string
	inUp, inBlock := false, false
	flush := func() {
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		buf = buf[:0]
		if hasCode(text) {
			stmts = append(stmts, text)
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		s := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(s, "-- +goose Up"):
			inUp = true
		case strings.HasPrefix(s, "-- +goose Down"):
			flush()
			return stmts
		case !inUp:
		case strings.HasPrefix(s, "-- +goose StatementBegin"):
			buf, inBlock = buf[:0], true
		case strings.HasPrefix(s, "-- +goose StatementEnd"):
			flush()
			inBlock = false
		case strings.HasPrefix(s, "-- +goose"):
		default:
			buf = append(buf, line)
			if !inBlock && strings.HasSuffix(s, ";") && !strings.HasPrefix(s, "--") {
				flush()
			}
		}
	}
	flush()
	return stmts
}

func hasCode(text string) bool {
	for _, ln := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(ln); t != "" && !strings.HasPrefix(t, "--") {
			return true
		}
	}
	return false
}

func stripSQLComments(stmt string) string {
	var keep []string
	for _, ln := range strings.Split(stmt, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(ln), "--") {
			keep = append(keep, ln)
		}
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

type sqlExecer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

// execSQLiteStatement runs one statement. `ADD COLUMN IF NOT EXISTS` becomes a
// plain ADD COLUMN, skipped when PRAGMA table_info already lists the column.
func execSQLiteStatement(ctx context.Context, x sqlExecer, stmt string) error {
	body := stripSQLComments(stmt)
	if m := addColumnIfNotExists.FindStringSubmatch(body); m != nil {
		table, col := strings.Trim(m[1], `"`), strings.Trim(m[2], `"`)
		has, err := sqliteHasColumn(ctx, x, table, col)
		if err != nil {
			return err
		}
		if has {
			return nil
		}
		body = fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", m[1], m[2], m[3])
	}
	_, err := x.ExecContext(ctx, body)
	return err
}

func sqliteHasColumn(ctx context.Context, x sqlExecer, table, col string) (bool, error) {
	rows, err := x.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if strings.EqualFold(name, col) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func ensureSQLiteVersionTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+sqliteVersionTable+` (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		version_id INTEGER NOT NULL,
		is_applied INTEGER NOT NULL,
		tstamp TIMESTAMP DEFAULT (datetime('now')))`)
	return err
}

func appliedSQLiteVersions(ctx context.Context, db *sql.DB) (map[int64]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version_id, is_applied FROM `+sqliteVersionTable+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[int64]bool{}
	for rows.Next() {
		var v int64
		var ok int
		if err := rows.Scan(&v, &ok); err != nil {
			return nil, err
		}
		applied[v] = ok != 0
	}
	return applied, rows.Err()
}

// applySQLiteMigrations applies every pending *.sqlite.sql twin and returns
// the versions it applied.
func applySQLiteMigrations(ctx context.Context, db *sql.DB, dir string) ([]int64, error) {
	if err := ensureSQLiteVersionTable(ctx, db); err != nil {
		return nil, err
	}
	migs, err := listSQLiteMigrations(dir)
	if err != nil {
		return nil, err
	}
	applied, err := appliedSQLiteVersions(ctx, db)
	if err != nil {
		return nil, err
	}
	// One connection for everything: PRAGMA foreign_keys is per connection.
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	var done []int64
	for _, m := range migs {
		if applied[m.version] {
			continue
		}
		if err := applyOneSQLite(ctx, conn, m); err != nil {
			return done, fmt.Errorf("migration %s: %w", m.name, err)
		}
		done = append(done, m.version)
	}
	return done, nil
}

func applyOneSQLite(ctx context.Context, conn *sql.Conn, m sqliteMigration) (err error) {
	raw, err := os.ReadFile(m.path)
	if err != nil {
		return err
	}
	stmts := append(append([]string{}, sqliteLegacyPrelude[m.version]...), upStatements(string(raw))...)
	// PRAGMA foreign_keys is a no-op inside a transaction, so a rebuild that
	// toggles it must run in autocommit mode.
	useTx := !pragmaForeignKeys.MatchString(string(raw))
	var exec sqlExecer = conn
	var tx *sql.Tx
	if useTx {
		if tx, err = conn.BeginTx(ctx, nil); err != nil {
			return err
		}
		exec = tx
		defer func() {
			if err != nil {
				_ = tx.Rollback()
			}
		}()
	}
	for _, s := range stmts {
		if err = execSQLiteStatement(ctx, exec, s); err != nil {
			return fmt.Errorf("%w\n  statement: %s", err, firstLine(s))
		}
	}
	if _, err = exec.ExecContext(ctx, `INSERT INTO `+sqliteVersionTable+` (version_id, is_applied) VALUES (?, 1)`, m.version); err != nil {
		return err
	}
	if tx != nil {
		return tx.Commit()
	}
	return nil
}

func firstLine(s string) string {
	body := stripSQLComments(s)
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[:i]
	}
	return body
}

// runSQLiteMigrate is the SQLite counterpart of the Postgres goose path.
func runSQLiteMigrate(ctx context.Context, dsn, dir, action string) error {
	switch action {
	case "up", "status", "version":
	case "down", "down-to":
		return errors.New("down migrations are not supported on SQLite; restore from a backup")
	default:
		return fmt.Errorf("unknown migrate action %q (try 'up', 'status', 'version', 'validate')", action)
	}
	db, err := sql.Open("sqlite", sqliteDriverPath(dsn))
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}
	if action == "up" {
		if err := refuseAheadOf(ctx, db, dir, true); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "migrate: applying sqlite migrations from %s\n", dir)
		done, err := applySQLiteMigrations(ctx, db, dir)
		fmt.Fprintf(os.Stderr, "migrate: applied %d migration(s)\n", len(done))
		return err
	}
	if err := ensureSQLiteVersionTable(ctx, db); err != nil {
		return err
	}
	applied, err := appliedSQLiteVersions(ctx, db)
	if err != nil {
		return err
	}
	if action == "version" {
		var highest int64 = -1
		for v, ok := range applied {
			if ok && v > highest {
				highest = v
			}
		}
		fmt.Fprintf(os.Stdout, "version: %d\n", highest)
		return nil
	}
	migs, err := listSQLiteMigrations(dir)
	if err != nil {
		return err
	}
	for _, m := range migs {
		state := "Pending"
		if applied[m.version] {
			state = "Applied"
		}
		fmt.Fprintf(os.Stdout, "%-8s %s\n", state, m.name)
	}
	return nil
}
