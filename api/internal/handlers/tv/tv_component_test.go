package tv

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	comps "github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// componentBase are the older slots' tables slot 0174 touches (the columns it reads).
const componentBase = `
CREATE TABLE cluster_nodes (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '');
CREATE TABLE audit_log (id INTEGER PRIMARY KEY AUTOINCREMENT, category TEXT NOT NULL, action TEXT NOT NULL,
	actor_user_id TEXT, target_id TEXT, payload TEXT NOT NULL DEFAULT '{}',
	ts TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), actor_source TEXT, target_kind TEXT);
CREATE TABLE job_stages (stage TEXT PRIMARY KEY, label TEXT NOT NULL, subject_kinds TEXT NOT NULL,
	pool TEXT NOT NULL, lane TEXT NOT NULL, lane_order INTEGER NOT NULL DEFAULT 0, version INTEGER NOT NULL DEFAULT 1,
	debounce_sec INTEGER NOT NULL DEFAULT 0, debounce_max_sec INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 3, edges TEXT NOT NULL DEFAULT '[]', may_request TEXT NOT NULL DEFAULT '[]',
	entry_for TEXT NOT NULL DEFAULT '[]', legacy INTEGER NOT NULL DEFAULT 0, hidden INTEGER NOT NULL DEFAULT 0,
	external_write INTEGER NOT NULL DEFAULT 0, write_mode TEXT NOT NULL DEFAULT 'auto',
	placement TEXT NOT NULL DEFAULT 'any', enabled INTEGER NOT NULL DEFAULT 1);
CREATE TABLE processing_jobs (id INTEGER PRIMARY KEY, stage TEXT NOT NULL)`

// componentTables adds slot 0174's SQLite twin (and what it needs) to db.
func componentTables(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, s := range strings.Split(componentBase, ";\n") {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "shared", "db", "migrations", "0174_components.sqlite.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up := string(b)
	if i := strings.Index(up, "-- +goose Down"); i >= 0 {
		up = up[:i]
	}
	for _, block := range strings.Split(up, "-- +goose StatementBegin") {
		if i := strings.Index(block, "-- +goose StatementEnd"); i >= 0 {
			if s := strings.TrimSpace(block[:i]); s != "" {
				if _, err := db.Exec(s); err != nil {
					t.Fatalf("0174: %v\n%s", err, s)
				}
			}
		}
	}
}

// T13: the TV settings' tuner switch is the livetv.hdhomerun component: each change
// writes its desired row and one audit row with the device, and a failure writes neither.
func TestHDHRSwitchIsTheComponent(t *testing.T) {
	db, srv := newServer(t, "")
	want := func(w string, audits int) {
		t.Helper()
		var got string
		if err := db.QueryRow(`SELECT want FROM components WHERE id = 'livetv.hdhomerun'`).Scan(&got); err != nil || got != w {
			t.Fatalf("want %q (%v), expected %q", got, err, w)
		}
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE category = 'components' AND target_id = 'livetv.hdhomerun'`).Scan(&n); err != nil || n != audits {
			t.Fatalf("%d audit rows (%v), want %d", n, err, audits)
		}
	}
	if code, out := do(t, srv, "PATCH", "/api/tv/hdhr", `{"enabled":true}`, admin); code != 200 {
		t.Fatalf("enable: %d %v", code, out)
	}
	want(comps.WantEnabled, 1)
	if code, out := do(t, srv, "PATCH", "/api/tv/hdhr", `{"enabled":false}`, admin); code != 200 || out["enabled"] != false {
		t.Fatalf("disable: %d %v", code, out)
	}
	want(comps.WantInstalled, 2)
	var by string
	if err := db.QueryRow(`SELECT updated_by FROM components WHERE id = 'livetv.hdhomerun'`).Scan(&by); err != nil || by != "a" {
		t.Fatalf("updated_by %q (%v), want the admin", by, err)
	}
	// a rename is not a switch: no component change
	if code, _ := do(t, srv, "PATCH", "/api/tv/hdhr", `{"friendly_name":"Den"}`, admin); code != 200 {
		t.Fatalf("rename: %d", code)
	}
	want(comps.WantInstalled, 2)

	// the component write fails: the device keeps its old value too (one transaction)
	if _, err := db.Exec(`DROP TABLE audit_log`); err != nil {
		t.Fatal(err)
	}
	if code, _ := do(t, srv, "PATCH", "/api/tv/hdhr", `{"enabled":true}`, admin); code != http.StatusInternalServerError {
		t.Fatalf("a failed component write answered %d", code)
	}
	var enabled bool
	if err := db.QueryRow(`SELECT enabled FROM hdhr_device`).Scan(&enabled); err != nil || enabled {
		t.Fatalf("hdhr_device.enabled = %v (%v) after a rolled-back change", enabled, err)
	}
}
