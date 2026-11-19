//go:build unix

package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// =====================================================================================
// 17 PostgreSQL 16 → 18 with data, then a rollback (Epic 49, story 49.2).
// =====================================================================================

// The seeded home on the previous release (PostgreSQL 16, its cluster in <root>/postgres)
// updates to a release whose payload has PostgreSQL 18 in postgres-18/ and whose watch
// fails (one migration more than its API takes): the start upgrades the cluster into
// <root>/postgres-18 with pg_upgrade, migrates it, and the rollback switches back to the
// previous release, which starts with its own code on its own 16 cluster, untouched, with
// every row and its own record. Then the home updates to a good release with PostgreSQL 18:
// the upgrade is made again (the 16 cluster moved on while it ran), the home serves it on 18
// with every row, pg_textsearch can be created, and the 16 cluster is still there as it was.
func TestUpdateRehearsal17PostgresMajorUpgrade(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	if k.pg18Home == "" {
		t.Fatal("MAKTABA_REHEARSAL_PG18_HOME is unset (tools/rehearsal/postgres18.sh)")
	}
	home, _ := r.homeOnPrev(profile("small"))
	db := home.superDB("maktaba")
	before := seedCounts(t, db)
	_ = db.Close()
	old := filepath.Join(home.root, "postgres")
	if got := pgVersionOf(t, old); got != "16" {
		t.Fatalf("the home's cluster before the update is PostgreSQL %q", got)
	}
	oldRecord, err := os.ReadFile(filepath.Join(home.root, "state", "database.json"))
	if err != nil {
		t.Fatalf("the previous release's record: %v", err)
	}

	// the release with PostgreSQL 18 whose watch fails: it rolls back to 16
	n1, b1 := additiveMigration(k.rev + 1)
	n2, b2 := additiveNextMigration(k.rev + 2)
	failing := payloadSpec{variant: "cur", version: k.cur, extra: map[string]string{n1: b1, n2: b2}, pg18: true}
	run, mon, _ := r.update(home, failing, 30*time.Minute)
	if run.State != "rolled_back" {
		t.Fatalf("the update to %s (PostgreSQL 18, failing) ended %s, want rolled_back: %s", k.cur, run.State, run.Error)
	}
	home.waitServing(k.prev, 8*time.Minute)
	r.budget(mon, "a rollback from PostgreSQL 18 to 16", 8*time.Minute)
	home.assertServing(k.prev)
	if got := pgVersionOf(t, filepath.Join(home.root, "postgres-18")); got != "18" {
		t.Errorf("the failing release did not upgrade a copy of the cluster to 18 (postgres-18: %q)", got)
	}
	if got := pgVersionOf(t, old); got != "16" {
		t.Errorf("the 16 cluster after the rollback is %q", got)
	}
	if now, _ := os.ReadFile(filepath.Join(home.root, "state", "database.json")); string(now) != string(oldRecord) {
		t.Errorf("the previous release's record changed across the upgrade and the rollback:\n%s\nwas\n%s", now, oldRecord)
	}
	db = home.superDB("maktaba")
	r.countsUnchanged(before, seedCounts(t, db))
	if ver := serverMajor(t, db); ver != "16" {
		t.Errorf("after the rollback the home serves PostgreSQL %s, want 16", ver)
	}
	_ = db.Close()

	// a good release with PostgreSQL 18: upgraded again, serving on 18
	m1, c1 := additiveMigration(k.rev + 1)
	good := payloadSpec{variant: "next", version: k.next, extra: map[string]string{m1: c1}, pg18: true}
	run, mon, _ = r.update(home, good, 30*time.Minute)
	if run.State != "committed" && run.State != "succeeded" {
		t.Fatalf("the update to %s (PostgreSQL 18) ended %s: %s", k.next, run.State, run.Error)
	}
	home.waitServing(k.next, 8*time.Minute)
	r.budget(mon, "an update from PostgreSQL 16 to 18", 8*time.Minute)
	db = home.superDB("maktaba")
	if ver := serverMajor(t, db); ver != "18" {
		t.Fatalf("the home serves PostgreSQL %s, want 18", ver)
	}
	r.countsUnchanged(before, seedCounts(t, db))
	if _, err := db.Exec("CREATE EXTENSION IF NOT EXISTS pg_textsearch"); err != nil {
		t.Errorf("pg_textsearch on the upgraded home: %v", err)
	}
	var preload string
	if err := db.QueryRow("SHOW shared_preload_libraries").Scan(&preload); err != nil || !strings.Contains(preload, "pg_textsearch") {
		t.Errorf("shared_preload_libraries = %q (%v)", preload, err)
	}
	if got := pgVersionOf(t, old); got != "16" {
		t.Errorf("the 16 cluster is gone after the upgrade (%q): a switch back would have nothing to start on", got)
	}
	var rec map[string]any
	if raw, err := os.ReadFile(filepath.Join(home.root, "state", "database-pg18.json")); err != nil || json.Unmarshal(raw, &rec) != nil {
		t.Errorf("the upgraded cluster's record: %v", err)
	} else if v, _ := rec["version"].(string); layoutVersion(v) != layoutVersion(k.next) {
		t.Errorf("the upgraded cluster's record is %v, want %s's", rec, k.next)
	}
}

// pgVersionOf is a cluster directory's PG_VERSION ("" when there is none).
func pgVersionOf(t *testing.T, data string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(data, "PG_VERSION"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// serverMajor is the major of the server db is connected to.
func serverMajor(t *testing.T, db interface {
	QueryRow(string, ...any) *sql.Row
}) string {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT current_setting('server_version_num')::int / 10000").Scan(&n); err != nil {
		t.Fatalf("the server's version: %v", err)
	}
	return strconv.Itoa(n)
}
