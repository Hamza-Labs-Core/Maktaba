package parental_test

// Slot 0222 goes up and down on Postgres and on its SQLite twin, the manifest lists
// it, and a machine's copy leaves its tables out. The checks name the slot's own
// objects: slots 0220 and 0221 keep theirs.

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

var slot0222Tables = []string{"parental_requests", "parental_pins", "parental_pin_failures", "parental_events"}

var slot0222Indexes = []string{
	"parental_requests_one_pending", "parental_requests_by_status_idx", "parental_pin_failures_child_idx",
	"parental_events_at_idx", "parental_events_child_idx", "parental_events_once",
}

func TestSlot0222UpAndDownOnPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t) // migrated to the head: 0222 is up
	up, down := gooseStatements(t, "0222_parental_requests.sql")
	count := func(q string, names []string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(q, "{"+strings.Join(names, ",")+"}").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	tables := func() int {
		return count(`SELECT count(*) FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = ANY($1::text[])`, slot0222Tables)
	}
	indexes := func() int {
		return count(`SELECT count(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname = ANY($1::text[])`, slot0222Indexes)
	}
	excluded := func() int {
		return count(`SELECT count(*) FROM unnest(cluster_copy_excluded()) AS t(name) WHERE name = ANY($1::text[])`, slot0222Tables)
	}
	names := func() map[string]bool {
		t.Helper()
		rows, err := db.Query(`SELECT unnest(cluster_copy_excluded())`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		got := map[string]bool{}
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			got[n] = true
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if tables() != len(slot0222Tables) || indexes() != len(slot0222Indexes) || excluded() != len(slot0222Tables) {
		t.Fatalf("after the migrations: %d tables, %d indexes, %d left out of a machine's copy",
			tables(), indexes(), excluded())
	}
	// the retention index is the one on `at`
	var def string
	if err := db.QueryRow(`SELECT indexdef FROM pg_indexes WHERE indexname = 'parental_events_at_idx'`).Scan(&def); err != nil ||
		!strings.Contains(def, "(at)") {
		t.Fatalf("parental_events_at_idx = %q (%v)", def, err)
	}
	// a PIN is never copied to a machine
	var copied bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM cluster_copy_tables() c WHERE c::text = 'parental_pins')`).Scan(&copied); err != nil || copied {
		t.Fatalf("parental_pins in cluster_copy_tables() = %v (%v)", copied, err)
	}

	run(t, db, "down", down)
	if tables() != 0 || indexes() != 0 || excluded() != 0 {
		t.Fatalf("after down: %d tables, %d indexes, %d names in the exclusion left", tables(), indexes(), excluded())
	}
	// Down restores slot 0204's list, checked against slot 0222's own Up below (not against
	// the head: a later slot's Up extends the list, and this Down leaves that slot's names
	// out too)
	afterDown := names()
	var kept bool
	if err := db.QueryRow(`SELECT 'plugin_entitlements' = ANY(cluster_copy_excluded())`).Scan(&kept); err != nil || !kept {
		t.Fatalf("slot 0204's exclusion after down: %v (%v)", kept, err)
	}
	// slot 0221 works without it
	if _, err := db.Exec(`SELECT 1 FROM parental_policies LIMIT 1`); err != nil {
		t.Fatalf("parental_policies after down: %v", err)
	}
	run(t, db, "up", up)
	if tables() != len(slot0222Tables) || indexes() != len(slot0222Indexes) || excluded() != len(slot0222Tables) {
		t.Fatalf("after up again: %d tables, %d indexes, %d excluded", tables(), indexes(), excluded())
	}
	// slot 0222's Up against its Down: the four names more, nothing else changed
	afterUp := names()
	for n := range afterDown {
		if !afterUp[n] {
			t.Errorf("%s is in the exclusion after down but not after up", n)
		}
	}
	if len(afterUp) != len(afterDown)+len(slot0222Tables) {
		t.Errorf("up has %d names, down %d: want the %d of slot 0222 between them", len(afterUp), len(afterDown), len(slot0222Tables))
	}
	run(t, db, "up twice", up) // every statement can run again
}

func TestSlot0222Constraints(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	refused := func(what, constraint, q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err == nil || !strings.Contains(err.Error(), constraint) {
			t.Fatalf("%s: %v, want %s", what, err, constraint)
		}
	}
	const child, guardian = "00000000-0000-4000-8000-000000220001", "00000000-0000-4000-8000-000000220002"
	must(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1,'m22-child','x',false), ($2,'m22-guardian','x',false)`, child, guardian)
	must(`INSERT INTO parental_policies (user_id, max_tier) VALUES ($1, 9)`, child)
	must(`INSERT INTO parental_requests (child_id, subject_kind, subject_id) VALUES ($1, 'video', 'v1')`, child)
	refused("a second pending request", "parental_requests_one_pending",
		`INSERT INTO parental_requests (child_id, subject_kind, subject_id) VALUES ($1, 'video', 'v1')`, child)
	// the kinds are a rule's (slot 0223 adds `time`: its own test looks at that)
	refused("an unknown kind", "parental_requests_subject_kind_valid",
		`INSERT INTO parental_requests (child_id, subject_kind, subject_id) VALUES ($1, 'page', 'p1')`, child)
	refused("an unknown status", "parental_requests_status_valid",
		`INSERT INTO parental_requests (child_id, subject_kind, subject_id, status) VALUES ($1, 'video', 'v2', 'open')`, child)
	must(`UPDATE parental_requests SET status = 'denied' WHERE child_id = $1`, child)
	must(`INSERT INTO parental_requests (child_id, subject_kind, subject_id) VALUES ($1, 'video', 'v1')`, child)
	refused("an unknown event", "parental_events_kind_valid",
		`INSERT INTO parental_events (child_id, kind) VALUES ($1, 'searched')`, child)
	must(`INSERT INTO parental_events (child_id, kind, subject_kind, subject_id, dedupe) VALUES ($1, 'blocked', 'video', 'v1', 'd1')`, child)
	refused("the same event twice", "parental_events_once",
		`INSERT INTO parental_events (child_id, kind, subject_kind, subject_id, dedupe) VALUES ($1, 'blocked', 'video', 'v1', 'd1')`, child)
	for _, kind := range []string{"time_up", "time_granted"} { // story 57.14's
		must(`INSERT INTO parental_events (child_id, kind) VALUES ($1, $2)`, child, kind)
	}
	must(`INSERT INTO parental_pins (guardian_id, pin_hash) VALUES ($1, 'x')`, guardian)
	must(`INSERT INTO parental_pin_failures (child_id, guardian_id) VALUES ($1, $2)`, child, guardian)
	// lifting the restriction takes the requests, the failures and the events
	must(`DELETE FROM parental_policies WHERE user_id = $1`, child)
	var left int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM parental_requests) + (SELECT count(*) FROM parental_events)
		+ (SELECT count(*) FROM parental_pin_failures)`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after lifting: %d rows left (%v)", left, err)
	}
	// deleting the guardian takes the PIN
	must(`DELETE FROM users WHERE id = $1`, guardian)
	if err := db.QueryRow(`SELECT count(*) FROM parental_pins`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after deleting the guardian: %d PINs left (%v)", left, err)
	}
}

func TestSlot0222OnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "p.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, dep := range []string{"0029_users.sqlite.sql", "0042_app_settings.sqlite.sql", "0221_parental_policies.sqlite.sql"} {
		depUp, _ := gooseStatements(t, dep)
		run(t, db, dep, depUp)
	}
	up, down := gooseStatements(t, "0222_parental_requests.sqlite.sql")
	run(t, db, "up", up)
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	must(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ('g','s-guardian','x',0), ('c','s-child','x',0)`)
	must(`INSERT INTO parental_policies (user_id, max_tier) VALUES ('c', 9)`)
	must(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ('r1', 'c', 'video', 'v1')`)
	if _, err := db.Exec(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ('r2', 'c', 'video', 'v1')`); err == nil {
		t.Fatal("a second pending request was stored")
	}
	if _, err := db.Exec(`INSERT INTO parental_events (id, child_id, kind) VALUES ('e0', 'c', 'searched')`); err == nil {
		t.Fatal("an unknown event kind was stored")
	}
	must(`INSERT INTO parental_events (id, child_id, kind, dedupe) VALUES ('e1', 'c', 'blocked', 'd')`)
	if _, err := db.Exec(`INSERT INTO parental_events (id, child_id, kind, dedupe) VALUES ('e2', 'c', 'blocked', 'd')`); err == nil {
		t.Fatal("the same event was stored twice")
	}
	must(`INSERT INTO parental_pins (guardian_id, pin_hash) VALUES ('g', 'x')`)
	must(`INSERT INTO parental_pin_failures (id, child_id, guardian_id) VALUES ('f1', 'c', 'g')`)
	must(`DELETE FROM parental_policies WHERE user_id = 'c'`)
	var left int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM parental_requests) + (SELECT count(*) FROM parental_events)
		+ (SELECT count(*) FROM parental_pin_failures)`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after lifting: %d rows left (%v)", left, err)
	}
	run(t, db, "down", down)
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('parental_requests', 'parental_pins',
		'parental_pin_failures', 'parental_events', 'parental_requests_one_pending', 'parental_requests_by_status_idx',
		'parental_pin_failures_child_idx', 'parental_events_at_idx', 'parental_events_child_idx', 'parental_events_once')`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after down: %d of the slot's objects left (%v)", left, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'parental_policies'`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("slot 0221's table after down: %d (%v)", left, err)
	}
	run(t, db, "up again", up)
}

func TestManifestListsSlot0222(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "MANIFEST.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "| `0222` | `0222_parental_requests.sql` |") {
		t.Fatal("MANIFEST.md has no row for slot 0222")
	}
	for _, f := range []string{"0222_parental_requests.sql", "0222_parental_requests.sqlite.sql"} {
		if _, err := os.Stat(filepath.Join(migrationsDir(t), f)); err != nil {
			t.Fatal(err)
		}
	}
}
