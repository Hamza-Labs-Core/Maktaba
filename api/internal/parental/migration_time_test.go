package parental_test

// 57.14 T07: slot 0223 goes up and down on Postgres and on its SQLite twin, and
// the manifest lists it. Only this slot's own tables and indexes are looked at.

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

var (
	slot0223Tables  = []string{"parental_time_rules", "parental_time_usage", "parental_time_grants"}
	slot0223Indexes = []string{"parental_time_rules_user_idx", "parental_time_grants_user_day_idx"}
)

func TestSlot0223UpAndDownOnPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t) // migrated to the head: 0223 is up
	up, down := gooseStatements(t, "0223_parental_time.sql")
	objects := func() int {
		var n int
		if err := db.QueryRow(`SELECT
			(SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = ANY($1::text[]))
			+ (SELECT count(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname = ANY($2::text[]))`,
			"{"+strings.Join(slot0223Tables, ",")+"}", "{"+strings.Join(slot0223Indexes, ",")+"}").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	want := len(slot0223Tables) + len(slot0223Indexes)
	if objects() != want {
		t.Fatalf("after the migrations: %d of the slot's %d tables and indexes", objects(), want)
	}
	// the slot before it stays whole when this one goes down
	policies := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'parental_policies'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	run(t, db, "down", down)
	if objects() != 0 || policies() != 1 {
		t.Fatalf("after down: %d of the slot's objects left, parental_policies %d", objects(), policies())
	}
	run(t, db, "up", up)
	if objects() != want {
		t.Fatalf("after up again: %d of the slot's objects", objects())
	}
	run(t, db, "up twice", up) // every statement can run again

	// the constraints the store relies on
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
	const child = "00000000-0000-4000-8000-000000572301"
	must(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1,'t-child','x',false)`, child)
	refused("a rule on an account with no policy", "parental_time_rules_user_id",
		`INSERT INTO parental_time_rules (user_id, days, daily_minutes) VALUES ($1, 127, 60)`, child)
	must(`INSERT INTO parental_policies (user_id, max_tier) VALUES ($1, 9)`, child)
	must(`INSERT INTO parental_time_rules (user_id, days, daily_minutes) VALUES ($1, 127, 60)`, child)
	must(`INSERT INTO parental_time_rules (user_id, days, slot_start, slot_end) VALUES ($1, 62, '21:00', '01:00')`, child)
	refused("no day", "parental_time_rules_days_valid", `INSERT INTO parental_time_rules (user_id, days, daily_minutes) VALUES ($1, 0, 60)`, child)
	refused("half a slot", "parental_time_rules_slot_whole", `INSERT INTO parental_time_rules (user_id, days, slot_start) VALUES ($1, 1, '16:00')`, child)
	refused("an empty slot", "parental_time_rules_slot_whole", `INSERT INTO parental_time_rules (user_id, days, slot_start, slot_end) VALUES ($1, 1, '16:00', '16:00')`, child)
	refused("a rule that says nothing", "parental_time_rules_not_empty", `INSERT INTO parental_time_rules (user_id, days) VALUES ($1, 1)`, child)
	must(`INSERT INTO parental_time_usage (user_id, day, seconds) VALUES ($1, '2026-11-10', 30)`, child)
	if err := exec(`INSERT INTO parental_time_usage (user_id, day, seconds) VALUES ($1, '2026-11-10', 30)`, child); err == nil {
		t.Fatal("one day and library was counted in two rows")
	}
	must(`INSERT INTO parental_time_grants (user_id, day, minutes) VALUES ($1, '2026-11-10', 30)`, child)
	if err := exec(`UPDATE parental_time_grants SET used_seconds = 1801 WHERE user_id = $1`, child); err == nil {
		t.Fatal("a grant was used past its minutes")
	}
	// lifting the restriction takes the rules, the count and the grants with it
	must(`DELETE FROM parental_policies WHERE user_id = $1`, child)
	var left int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM parental_time_rules WHERE user_id = $1)
		+ (SELECT count(*) FROM parental_time_usage WHERE user_id = $1)
		+ (SELECT count(*) FROM parental_time_grants WHERE user_id = $1)`, child).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after lifting: %d rows left (%v)", left, err)
	}
}

// Slot 0223 adds the request kind `time` to slot 0222's parental_requests by
// replacing one check; down deletes the requests for time and restores the check.
func TestSlot0223RequestKindTime(t *testing.T) {
	db, _ := clustertest.ScratchDB(t) // migrated to the head: 0223 is up
	up, down := gooseStatements(t, "0223_parental_time.sql")
	exec := func(q string, args ...any) error { _, err := db.Exec(q, args...); return err }
	must := func(q string, args ...any) {
		t.Helper()
		if err := exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	refused := func(what, q string, args ...any) {
		t.Helper()
		if err := exec(q, args...); err == nil || !strings.Contains(err.Error(), "parental_requests_subject_kind_valid") {
			t.Fatalf("%s: %v, want parental_requests_subject_kind_valid", what, err)
		}
	}
	check := func() (def string, valid bool) {
		t.Helper()
		if err := db.QueryRow(`SELECT pg_get_constraintdef(oid), convalidated FROM pg_constraint
			WHERE conname = 'parental_requests_subject_kind_valid' AND conrelid = 'parental_requests'::regclass`).Scan(&def, &valid); err != nil {
			t.Fatal(err)
		}
		return def, valid
	}
	rows := func(where string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM parental_requests WHERE ` + where).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	const child = "00000000-0000-4000-8000-000000572302"
	const ins = `INSERT INTO parental_requests (child_id, subject_kind, subject_id, status) VALUES ($1, $2, $3, $4)`
	must(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1,'t-asks','x',false)`, child)
	must(`INSERT INTO parental_policies (user_id, max_tier) VALUES ($1, 9)`, child)

	if def, valid := check(); !strings.Contains(def, "'time'") || !valid {
		t.Fatalf("after the migrations the check is %q (validated %v)", def, valid)
	}
	must(ins, child, "time", "30", "pending")
	must(ins, child, "time", "15", "approved")
	must(ins, child, "video", "v1", "pending")
	refused("an unknown kind", ins, child, "page", "p1", "pending")

	// down: the requests for time go, waiting or answered, and the check is slot 0222's
	run(t, db, "down", down)
	if rows(`subject_kind = 'time'`) != 0 || rows(`subject_kind = 'video'`) != 1 {
		t.Fatalf("after down: %d requests for time and %d for a video, want 0 and 1",
			rows(`subject_kind = 'time'`), rows(`subject_kind = 'video'`))
	}
	if def, valid := check(); strings.Contains(def, "'time'") || !strings.Contains(def, "'video'") || !valid {
		t.Fatalf("after down the check is %q (validated %v)", def, valid)
	}
	refused("a request for time after down", ins, child, "time", "30", "pending")
	must(ins, child, "library", "l1", "pending")
	run(t, db, "down twice", down)

	run(t, db, "up", up)
	if def, valid := check(); !strings.Contains(def, "'time'") || !valid {
		t.Fatalf("after up again the check is %q (validated %v)", def, valid)
	}
	must(ins, child, "time", "30", "pending")
	refused("an unknown kind after up", ins, child, "page", "p1", "pending")
	run(t, db, "up twice", up) // every statement can run again, and keeps the rows
	if rows(`true`) != 3 {
		t.Fatalf("%d requests after up twice, want 3", rows(`true`))
	}
	// slot 0222's indexes are untouched
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pg_indexes WHERE schemaname = 'public'
		AND indexname IN ('parental_requests_one_pending', 'parental_requests_by_status_idx')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("slot 0222's indexes on parental_requests: %d (%v)", n, err)
	}
}

func TestSlot0223OnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "p.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, dep := range []string{"0001_init_libraries_and_videos.sqlite.sql", "0029_users.sqlite.sql",
		"0042_app_settings.sqlite.sql", "0221_parental_policies.sqlite.sql", "0222_parental_requests.sqlite.sql"} {
		depUp, _ := gooseStatements(t, dep)
		run(t, db, dep, depUp)
	}
	up, down := gooseStatements(t, "0223_parental_time.sqlite.sql")
	// a request from before the slot is carried over when the table is built again
	if _, err := db.Exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ('c0','s-early','x',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO parental_policies (user_id, max_tier) VALUES ('c0', 9)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id, status) VALUES ('q0', 'c0', 'video', 'v0', 'denied')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ('q-early', 'c0', 'time', '30')`); err == nil {
		t.Fatal("slot 0222 alone stored a request for time")
	}
	run(t, db, "up", up)
	exec := func(q string, args ...any) error { _, err := db.Exec(q, args...); return err }
	must := func(q string, args ...any) {
		t.Helper()
		if err := exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	must(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ('g','s-guardian','x',0), ('c','s-child','x',0)`)
	must(`INSERT INTO parental_policies (user_id, max_tier) VALUES ('c', 9)`)
	must(`INSERT INTO parental_time_rules (id, user_id, days, daily_minutes) VALUES ('r1', 'c', 127, 60)`)
	must(`INSERT INTO parental_time_rules (id, user_id, days, slot_start, slot_end) VALUES ('r2', 'c', 62, '21:00:00', '01:00:00')`)
	for what, q := range map[string]string{
		"no day":                   `INSERT INTO parental_time_rules (id, user_id, days, daily_minutes) VALUES ('x', 'c', 0, 60)`,
		"half a slot":              `INSERT INTO parental_time_rules (id, user_id, days, slot_start) VALUES ('x', 'c', 1, '16:00:00')`,
		"a rule that says nothing": `INSERT INTO parental_time_rules (id, user_id, days) VALUES ('x', 'c', 1)`,
		"a rule with no policy":    `INSERT INTO parental_time_rules (id, user_id, days, daily_minutes) VALUES ('x', 'g', 1, 60)`,
	} {
		if err := exec(q); err == nil {
			t.Fatalf("%s was stored", what)
		}
	}
	must(`INSERT INTO parental_time_usage (user_id, day, seconds) VALUES ('c', '2026-11-10', 30)`)
	if err := exec(`INSERT INTO parental_time_usage (user_id, day, seconds) VALUES ('c', '2026-11-10', 30)`); err == nil {
		t.Fatal("one day and library was counted in two rows")
	}
	must(`INSERT INTO parental_time_grants (id, user_id, day, minutes, granted_by) VALUES ('g1', 'c', '2026-11-10', 30, 'g')`)
	if err := exec(`UPDATE parental_time_grants SET used_seconds = 1801 WHERE id = 'g1'`); err == nil {
		t.Fatal("a grant was used past its minutes")
	}
	// the request kind `time`, on the table built again: the old row, the one pending
	// request per subject and the cascade from the policy are all kept
	var left int
	if err := db.QueryRow(`SELECT count(*) FROM parental_requests WHERE id = 'q0' AND status = 'denied'`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("the request from before the slot: %d (%v)", left, err)
	}
	must(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ('q1', 'c', 'time', '30')`)
	must(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ('q2', 'c', 'video', 'v1')`)
	must(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ('q3', 'c0', 'time', '15')`)
	for what, q := range map[string]string{
		"an unknown kind":          `INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ('x', 'c', 'page', 'p')`,
		"a second pending request": `INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ('x', 'c', 'time', '30')`,
		"an unknown status":        `INSERT INTO parental_requests (id, child_id, subject_kind, subject_id, status) VALUES ('x', 'c', 'video', 'v9', 'open')`,
	} {
		if err := exec(q); err == nil {
			t.Fatalf("%s was stored", what)
		}
	}
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('parental_requests_one_pending',
		'parental_requests_by_status_idx', 'parental_requests_0223')`).Scan(&left); err != nil || left != 2 {
		t.Fatalf("the indexes of parental_requests after the slot: %d of 2 (%v)", left, err)
	}
	must(`DELETE FROM parental_policies WHERE user_id = 'c'`)
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM parental_time_rules) + (SELECT count(*) FROM parental_time_usage)
		+ (SELECT count(*) FROM parental_time_grants) + (SELECT count(*) FROM parental_requests WHERE child_id = 'c')`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after lifting: %d rows left (%v)", left, err)
	}
	run(t, db, "down", down)
	// down: the request for time is gone, the other is kept, and `time` is refused again
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM parental_requests WHERE subject_kind = 'time') * 10
		+ (SELECT count(*) FROM parental_requests WHERE id = 'q0')`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("after down: requests = %d, want only the one from before the slot (%v)", left, err)
	}
	if err := exec(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ('q4', 'c0', 'time', '30')`); err == nil {
		t.Fatal("a request for time was stored after down")
	}
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('parental_requests', 'parental_requests_one_pending',
		'parental_requests_by_status_idx')`).Scan(&left); err != nil || left != 3 {
		t.Fatalf("slot 0222's parental_requests after down: %d of 3 objects (%v)", left, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('parental_time_rules', 'parental_time_usage',
		'parental_time_grants', 'parental_time_rules_user_idx', 'parental_time_grants_user_day_idx')`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after down: %d of the slot's objects left (%v)", left, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'parental_policies'`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("after down: parental_policies %d (%v)", left, err)
	}
	run(t, db, "up again", up)
}

func TestManifestListsSlot0223(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "MANIFEST.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "| `0223` | `0223_parental_time.sql` |") {
		t.Fatal("MANIFEST.md has no row for slot 0223")
	}
	for _, f := range []string{"0223_parental_time.sql", "0223_parental_time.sqlite.sql"} {
		if _, err := os.Stat(filepath.Join(migrationsDir(t), f)); err != nil {
			t.Fatal(err)
		}
	}
}
