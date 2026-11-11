package parental_test

// T08: slot 0221 goes up and down on Postgres and on its SQLite twin, and the
// manifest lists it.

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

func migrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the migrations")
	}
	// api/internal/parental/migration_test.go -> repo root
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "shared", "db", "migrations")
}

// gooseStatements returns a migration file's Up and Down statements.
func gooseStatements(t *testing.T, name string) (up, down []string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), name))
	if err != nil {
		t.Fatal(err)
	}
	var cur *[]string
	var stmt []string
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		switch strings.TrimSpace(line) {
		case "-- +goose Up":
			cur = &up
		case "-- +goose Down":
			cur = &down
		case "-- +goose StatementBegin":
			in, stmt = true, nil
		case "-- +goose StatementEnd":
			if cur == nil {
				t.Fatalf("%s: a statement before Up or Down", name)
			}
			*cur = append(*cur, strings.Join(stmt, "\n"))
			in = false
		default:
			if in {
				stmt = append(stmt, line)
			}
		}
	}
	if len(up) == 0 || len(down) == 0 {
		t.Fatalf("%s: %d up and %d down statements", name, len(up), len(down))
	}
	return up, down
}

func run(t *testing.T, db *sql.DB, what string, stmts []string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v\n%s", what, err, s)
		}
	}
}

var slot0221Tables = []string{"parental_policies", "parental_guardians", "parental_rules", "session_age_signals"}

func TestSlot0221UpAndDownOnPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t) // migrated to the head: 0221 is up
	up, down := gooseStatements(t, "0221_parental_policies.sql")
	tables := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = ANY($1::text[])`,
			"{"+strings.Join(slot0221Tables, ",")+"}").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	leftovers := func() int {
		var n int
		if err := db.QueryRow(`SELECT
			(SELECT count(*) FROM pg_proc WHERE proname LIKE 'parental\_%' OR proname = 'users_restricted_admin_guard')
			+ (SELECT count(*) FROM pg_trigger WHERE tgname = 'users_restricted_admin_guard_trg')
			+ (SELECT count(*) FROM app_settings WHERE key LIKE 'parental.%')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if tables() != len(slot0221Tables) {
		t.Fatalf("after the migrations: %d of the slot's tables", tables())
	}
	var enabled string
	if err := db.QueryRow(`SELECT value::text FROM app_settings WHERE key = 'parental.enabled'`).Scan(&enabled); err != nil || enabled != "true" {
		t.Fatalf("parental.enabled = %q (%v), want true", enabled, err)
	}
	run(t, db, "down", down)
	if tables() != 0 || leftovers() != 0 {
		t.Fatalf("after down: %d tables and %d functions, triggers or settings left", tables(), leftovers())
	}
	// the users table works without the slot's trigger
	if _, err := db.Exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES (gen_random_uuid(),'m-user','x',false)`); err != nil {
		t.Fatalf("users after down: %v", err)
	}
	if _, err := db.Exec(`UPDATE users SET is_admin = true WHERE username = 'm-user'`); err != nil {
		t.Fatalf("users after down: %v", err)
	}
	run(t, db, "up", up)
	if tables() != len(slot0221Tables) {
		t.Fatalf("after up again: %d of the slot's tables", tables())
	}
	run(t, db, "up twice", up) // every statement can run again
}

func TestSlot0221OnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "p.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, dep := range []string{"0029_users.sqlite.sql", "0042_app_settings.sqlite.sql"} {
		depUp, _ := gooseStatements(t, dep)
		run(t, db, dep, depUp)
	}
	up, down := gooseStatements(t, "0221_parental_policies.sqlite.sql")
	run(t, db, "up", up)
	exec := func(q string, args ...any) error { _, err := db.Exec(q, args...); return err }
	must := func(q string, args ...any) {
		t.Helper()
		if err := exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	refused := func(what, constraint, q string, args ...any) {
		t.Helper()
		if err := exec(q, args...); err == nil || !strings.Contains(err.Error(), constraint) {
			t.Fatalf("%s: %v, want %s", what, err, constraint)
		}
	}
	must(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ('a','s-admin','x',1), ('g','s-guardian','x',0), ('c','s-child','x',0), ('d','s-child-two','x',0)`)
	refused("policy on an admin", "parental_restricted_admin", `INSERT INTO parental_policies (user_id, max_tier) VALUES ('a', 9)`)
	must(`INSERT INTO parental_policies (user_id, max_tier) VALUES ('c', 9), ('d', 13)`)
	refused("admin on a restricted account", "parental_restricted_admin", `UPDATE users SET is_admin = 1 WHERE id = 'c'`)
	refused("a restricted guardian", "parental_restricted_guardian", `INSERT INTO parental_guardians (child_id, guardian_id) VALUES ('c', 'd')`)
	must(`INSERT INTO parental_guardians (child_id, guardian_id) VALUES ('c', 'g')`)
	refused("policy on a guardian", "parental_restricted_guardian", `INSERT INTO parental_policies (user_id, max_tier) VALUES ('g', 9)`)
	if err := exec(`INSERT INTO parental_policies (user_id, max_tier) VALUES ('g', 12)`); err == nil {
		t.Fatal("tier 12 was stored")
	}
	must(`INSERT INTO parental_rules (id, user_id, effect, subject_kind, subject_id) VALUES ('r1', 'c', 'block', 'video', 'v1')`)
	if err := exec(`INSERT INTO parental_rules (id, user_id, effect, subject_kind, subject_id) VALUES ('r2', 'c', 'block', 'video', 'v1')`); err == nil {
		t.Fatal("the same rule was stored twice")
	}
	must(`INSERT INTO session_age_signals (refresh_family_id, user_id, tier_limit) VALUES ('f1', 'c', 9)`)
	// lifting takes the guardians and rules with it
	must(`DELETE FROM parental_policies WHERE user_id = 'c'`)
	var left int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM parental_guardians) + (SELECT count(*) FROM parental_rules)`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after lifting: %d guardians and rules left (%v)", left, err)
	}
	must(`UPDATE users SET is_admin = 1 WHERE id = 'c'`)
	var setting string
	if err := db.QueryRow(`SELECT value FROM app_settings WHERE key = 'parental.unrated_on_signal'`).Scan(&setting); err != nil || setting != `"hide"` {
		t.Fatalf("parental.unrated_on_signal = %q (%v)", setting, err)
	}

	run(t, db, "down", down)
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name LIKE 'parental%' OR name LIKE 'session_age%'
		OR name = 'users_restricted_admin_guard'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after down: %d of the slot's objects left (%v)", left, err)
	}
	must(`UPDATE users SET is_admin = 1 WHERE id = 'd'`) // the users table works without the trigger
	run(t, db, "up again", up)
}

func TestManifestListsSlot0221(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "MANIFEST.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "| `0221` | `0221_parental_policies.sql` |") {
		t.Fatal("MANIFEST.md has no row for slot 0221")
	}
	for _, f := range []string{"0221_parental_policies.sql", "0221_parental_policies.sqlite.sql"} {
		if _, err := os.Stat(filepath.Join(migrationsDir(t), f)); err != nil {
			t.Fatal(err)
		}
	}
}
