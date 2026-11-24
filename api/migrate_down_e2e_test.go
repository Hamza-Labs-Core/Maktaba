//go:build unix

package main

// Every migration's Down script runs after any part of its Up, and takes the schema back
// to what it was before it (the update rehearsal's kit job runs this on the bundled
// PostgreSQL: docs/update-rehearsal.md).
//
// A failed update is undone by its migrations' own Down scripts, and the whole dump goes
// back only when they fail (cmd/maktaba-server/internal/updater/failedback.go). On
// 2026-11-23 and 2026-11-24 the database step put the 34 GB dump back after
// 0235_search_terms.sql's CREATE INDEX CONCURRENTLY was cancelled part way, about three
// hours down each time. This proves the way back that replaces it, on each migration
// above MAKTABA_DOWN_FROM (default 0230, the newest slot of 0.3.153, the release the
// owner's home runs):
//
//   - a NO TRANSACTION migration cut after each of its statements (0 to n-1 ran, the
//     next one failed): `migrate undo-failed` (runUndoFailed) leaves the schema as it was
//     before the migration;
//   - every migration run whole, then its Down script (goose down): the schema as it was
//     before; then up again, which the next migration starts from.
//
// The schema is compared by its catalog: tables' columns, indexes, constraints, functions
// (with a hash of their bodies), triggers, views, text search configurations and
// dictionaries, sequences and types of the public schema. Rows are not: a seed the Down
// leaves (a job stage, which the queue's rows name) is by design.
//
// It needs MAKTABA_TEST_PG_HOME, a PostgreSQL install with pgvector (bin/initdb, pg_ctl,
// postgres: the Linux payload's postgres-18, as the rehearsal's kit fetches it). It makes
// its own cluster in a temporary folder and stops it at the end.

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

// schemaFingerprintSQL lists the public schema's objects, one line each.
const schemaFingerprintSQL = `
SELECT 'column ' || table_name || '.' || column_name || ' ' || data_type || ' null=' || is_nullable ||
       ' default=' || coalesce(column_default, '') || ' generated=' || coalesce(generation_expression, '')
  FROM information_schema.columns WHERE table_schema = 'public'
UNION ALL SELECT 'index ' || indexname || ' ' || indexdef FROM pg_indexes WHERE schemaname = 'public'
UNION ALL SELECT 'invalid index ' || c.relname FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
  WHERE NOT i.indisvalid AND c.relnamespace = 'public'::regnamespace
UNION ALL SELECT 'constraint ' || conrelid::regclass || ' ' || conname || ' ' || pg_get_constraintdef(oid)
  FROM pg_constraint WHERE connamespace = 'public'::regnamespace
UNION ALL SELECT 'function ' || p.oid::regprocedure || ' ' || md5(p.prosrc)
  FROM pg_proc p WHERE p.pronamespace = 'public'::regnamespace
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')
UNION ALL SELECT 'trigger ' || tgrelid::regclass || ' ' || tgname FROM pg_trigger WHERE NOT tgisinternal
UNION ALL SELECT 'view ' || viewname || ' ' || md5(definition) FROM pg_views WHERE schemaname = 'public'
UNION ALL SELECT 'ts config ' || cfgname FROM pg_ts_config WHERE cfgnamespace = 'public'::regnamespace
UNION ALL SELECT 'ts dictionary ' || dictname FROM pg_ts_dict WHERE dictnamespace = 'public'::regnamespace
UNION ALL SELECT 'sequence ' || sequencename FROM pg_sequences WHERE schemaname = 'public'
UNION ALL SELECT 'type ' || t.typname FROM pg_type t WHERE t.typnamespace = 'public'::regnamespace
   AND t.typtype IN ('e', 'd')
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = t.oid AND d.deptype = 'e')
ORDER BY 1`

func schemaFingerprint(ctx context.Context, t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx, schemaFingerprintSQL)
	if err != nil {
		t.Fatalf("the schema's catalog: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// schemaDiff is what is in a and not b ("-") and in b and not a ("+").
func schemaDiff(a, b []string) []string {
	in := func(xs []string) map[string]bool {
		m := map[string]bool{}
		for _, x := range xs {
			m[x] = true
		}
		return m
	}
	ia, ib := in(a), in(b)
	var out []string
	for _, x := range a {
		if !ib[x] {
			out = append(out, "- "+x)
		}
	}
	for _, x := range b {
		if !ia[x] {
			out = append(out, "+ "+x)
		}
	}
	return out
}

var gooseStmtBegin = regexp.MustCompile(`^\s*--\s*\+goose\s+StatementBegin\b`)
var gooseStmtEnd = regexp.MustCompile(`^\s*--\s*\+goose\s+StatementEnd\b`)
var gooseUpLine = regexp.MustCompile(`^\s*--\s*\+goose\s+Up\b`)
var gooseDownLine = regexp.MustCompile(`^\s*--\s*\+goose\s+Down\b`)

// gooseUpStatements are the statements of a migration's Up section as goose runs them: a
// StatementBegin/End block is one, else a statement ends on a line that ends with ";".
func gooseUpStatements(raw string) []string {
	var (
		out     []string
		cur     strings.Builder
		inUp    bool
		inBlock bool
	)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" && strings.TrimSpace(stripSQLComments(s)) != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case gooseUpLine.MatchString(line):
			inUp = true
			continue
		case gooseDownLine.MatchString(line):
			flush()
			return out
		case !inUp:
			continue
		case gooseStmtBegin.MatchString(line):
			flush()
			inBlock = true
			continue
		case gooseStmtEnd.MatchString(line):
			flush()
			inBlock = false
			continue
		}
		cur.WriteString(line + "\n")
		if !inBlock {
			code := line
			if i := strings.Index(code, "--"); i >= 0 {
				code = code[:i]
			}
			if strings.HasSuffix(strings.TrimSpace(code), ";") {
				flush()
			}
		}
	}
	flush()
	return out
}

// scratchPostgres starts a cluster of home's binaries in a temporary folder (stopped at
// the end) and opens its postgres database.
func scratchPostgres(t *testing.T, home string) *sql.DB {
	t.Helper()
	bin := filepath.Join(home, "bin")
	dir, err := os.MkdirTemp("", "maktaba-down-pg-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	data, sock := filepath.Join(dir, "data"), filepath.Join(dir, "s")
	if err := os.MkdirAll(sock, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(name string, args ...string) {
		cmd := exec.Command(filepath.Join(bin, name), args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	run("initdb", "-D", data, "-U", "postgres", "--auth=trust", "--locale=C", "--encoding=UTF8")
	port := strconv.Itoa(freePortDown(t))
	run("pg_ctl", "-D", data, "-l", filepath.Join(dir, "pg.log"), "-w", "-o",
		"-p "+port+" -k "+sock+" -c listen_addresses=''", "start")
	t.Cleanup(func() {
		cmd := exec.Command(filepath.Join(bin, "pg_ctl"), "-D", data, "-m", "fast", "-w", "stop")
		_ = cmd.Run()
	})
	db, err := sql.Open("postgres", fmt.Sprintf("host=%s port=%s user=postgres dbname=postgres sslmode=disable", sock, port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(4)
	return db
}

func freePortDown(t *testing.T) int {
	t.Helper()
	// a port no cluster of this machine uses: the scratch cluster listens on its socket only
	return 56000 + os.Getpid()%2000
}

func TestMigrationsDownAfterPartialUp(t *testing.T) {
	home := os.Getenv("MAKTABA_TEST_PG_HOME")
	if home == "" {
		t.Skip("MAKTABA_TEST_PG_HOME: a PostgreSQL install with pgvector (the Linux payload's postgres-18)")
	}
	from := int64(230)
	if v := os.Getenv("MAKTABA_DOWN_FROM"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatalf("MAKTABA_DOWN_FROM %q: %v", v, err)
		}
		from = n
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	db := scratchPostgres(t, home)
	dir := defaultMigrationsDir()
	if _, err := os.Stat(filepath.Join(dir, "0001_init_libraries_and_videos.sql")); err != nil {
		dir = filepath.Join("..", "shared", "db", "migrations")
	}
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	goose.SetLogger(goose.NopLogger())
	if err := bootstrapSchemaVersion(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	stage, err := stagePostgresMigrations(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	if err := goose.UpToContext(ctx, db, stage, from); err != nil {
		t.Fatalf("up to %04d: %v", from, err)
	}
	ms, err := goose.CollectMigrations(stage, from, goose.MaxVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatalf("no migration above %04d", from)
	}
	for _, m := range ms {
		name := filepath.Base(m.Source)
		raw, err := os.ReadFile(m.Source)
		if err != nil {
			t.Fatal(err)
		}
		before := schemaFingerprint(ctx, t, db)
		if noTransactionMigration.Match(raw) {
			stmts := gooseUpStatements(string(raw))
			if len(stmts) == 0 {
				t.Fatalf("%s: no Up statement read", name)
			}
			for k := 0; k < len(stmts); k++ {
				// statements 1..k ran, statement k+1 failed
				for i := 0; i < k; i++ {
					if _, err := db.ExecContext(ctx, stmts[i]); err != nil {
						t.Fatalf("%s: Up statement %d of %d: %v", name, i+1, len(stmts), err)
					}
				}
				// the failure of the owner's home: statement k+1, a CREATE INDEX CONCURRENTLY,
				// cancelled by its lock timeout, leaves its index INVALID
				if idx := concurrentIndexesOf("-- +goose Up\n" + stmts[k]); len(idx) == 1 {
					if _, err := db.ExecContext(ctx, stmts[k]); err != nil {
						t.Fatalf("%s: Up statement %d of %d: %v", name, k+1, len(stmts), err)
					}
					if _, err := db.ExecContext(ctx, `UPDATE pg_index SET indisvalid = false WHERE indexrelid = to_regclass($1)`,
						idx[0]); err != nil {
						t.Fatal(err)
					}
				}
				if err := runUndoFailed(ctx, db, dir); err != nil {
					t.Fatalf("%s: its Down script after %d of its %d Up statements: %v", name, k, len(stmts), err)
				}
				if d := schemaDiff(before, schemaFingerprint(ctx, t, db)); len(d) > 0 {
					t.Fatalf("%s: after %d of its %d Up statements, its Down script leaves the schema changed:\n%s",
						name, k, len(stmts), strings.Join(d, "\n"))
				}
			}
			t.Logf("%s: its Down script undoes each of its %d partial Ups", name, len(stmts))
		}
		// whole, then its Down script, then whole again (the next one starts from it)
		if err := goose.UpByOneContext(ctx, db, stage); err != nil {
			t.Fatalf("%s: up: %v", name, err)
		}
		if err := goose.DownContext(ctx, db, stage); err != nil {
			t.Fatalf("%s: its Down script after its whole Up: %v", name, err)
		}
		if d := schemaDiff(before, schemaFingerprint(ctx, t, db)); len(d) > 0 {
			t.Fatalf("%s: after its whole Up, its Down script leaves the schema changed:\n%s", name, strings.Join(d, "\n"))
		}
		if err := goose.UpByOneContext(ctx, db, stage); err != nil {
			t.Fatalf("%s: up again after its Down script: %v", name, err)
		}
		t.Logf("%s: whole, its Down script undoes it", name)
	}
}

// The failure of 2026-11-23 and 2026-11-24 on the owner's home, end to end: 0.3.153's
// database (0230), the update's 0231 to 0234 run, 0235 cut at its third statement (its
// table and trigram index made, the term_prefix build cancelled by its lock timeout and
// left INVALID). The way back the database step takes (`migrate undo-failed`, then
// `migrate down-to 230`) leaves the schema exactly as 0.3.153 knows it, with no dump.
func TestHomeFailureOf0235UndoneByDownScripts(t *testing.T) {
	home := os.Getenv("MAKTABA_TEST_PG_HOME")
	if home == "" {
		t.Skip("MAKTABA_TEST_PG_HOME: a PostgreSQL install with pgvector (the Linux payload's postgres-18)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	db := scratchPostgres(t, home)
	dir := filepath.Join("..", "shared", "db", "migrations")
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	goose.SetLogger(goose.NopLogger())
	if err := bootstrapSchemaVersion(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	stage, err := stagePostgresMigrations(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	if err := goose.UpToContext(ctx, db, stage, 230); err != nil {
		t.Fatal(err)
	}
	v153 := schemaFingerprint(ctx, t, db)
	if err := goose.UpToContext(ctx, db, stage, 234); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "0235_search_terms.sql"))
	if err != nil {
		t.Fatal(err)
	}
	stmts := gooseUpStatements(string(raw))
	if len(stmts) < 3 || !strings.Contains(stmts[2], "search_terms_term_prefix") {
		t.Fatalf("0235's third statement is not the term_prefix build: %q", stmts)
	}
	for _, st := range stmts[:3] {
		if _, err := db.ExecContext(ctx, st); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE pg_index SET indisvalid = false WHERE indexrelid = 'search_terms_term_prefix'::regclass`); err != nil {
		t.Fatal(err)
	}
	if err := runUndoFailed(ctx, db, dir); err != nil {
		t.Fatalf("undo-failed: %v", err)
	}
	if err := runGooseDownTo(ctx, db, dir, 230); err != nil {
		t.Fatalf("down-to 230: %v", err)
	}
	if d := schemaDiff(v153, schemaFingerprint(ctx, t, db)); len(d) > 0 {
		t.Fatalf("the schema is not 0.3.153's after the Down scripts:\n%s", strings.Join(d, "\n"))
	}
	if v, err := goose.GetDBVersionContext(ctx, db); err != nil || v != 230 {
		t.Fatalf("goose's version %d (%v), want 230", v, err)
	}
}
