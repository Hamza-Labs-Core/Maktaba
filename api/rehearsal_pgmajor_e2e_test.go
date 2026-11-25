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
//
// The home has a machine's copy published; on a release from before slot 0237 also its tables
// with a primary key at REPLICA IDENTITY FULL, transcript_segments with its generated search_tsv
// among them, as the home that moved to 18 on 2026-11-20. On 18 the second listen's writes to it must go
// through (slot 0237): PostgreSQL 18 refused them, and every second_listen job failed, until
// then. And every table with a primary key has it as its identity again (slot 0238), while
// one without keeps FULL.
//
// A release with 0237 and 0238 does not run them again, and nothing in the product makes a table
// with a primary key FULL (a machine's publication and a switchover's do it only where there is
// none): a home on one cannot be in that state, so it is not set up there.
func TestUpdateRehearsal17PostgresMajorUpgrade(t *testing.T) {
	r := newRehearsal(t)
	k := r.kit
	if k.pg18Home == "" {
		t.Fatal("MAKTABA_REHEARSAL_PG18_HOME is unset (tools/rehearsal/postgres18.sh)")
	}
	home, _ := r.homeOnPrev(profile("small"))
	db := home.superDB("maktaba")
	before := seedCounts(t, db)
	pub := publishedAsTheHomeWas(t, db, newestSlot(filepath.Join(k.dir, k.prevVariant, "migrations")) < 237)
	_ = db.Close()
	old := filepath.Join(home.root, "postgres")
	if got := pgVersionOf(t, old); got != "16" {
		t.Fatalf("the home's cluster before the update is PostgreSQL %q", got)
	}
	if _, err := os.Stat(filepath.Join(home.root, "state", "database.json")); err != nil {
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
	// the previous release's own record: still its own and migrated, with no restore of
	// any kind marked (the release before writes its pre-update dump's name into it, next and
	// next_dump, before the switch: that is its own doing)
	if rec, all := home.record(); layoutVersion(rec.Version) != layoutVersion(k.prev) || rec.State != "migrated" ||
		all["failed"] != nil || all["restore_of"] != nil {
		t.Errorf("the previous release's record after the rollback is %v: want %s's own, migrated, nothing to put back",
			all, k.prev)
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
	if run.State != "done" && run.State != "committed" {
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
	secondListenStores(t, db, pub)
	identitiesAfterTheUpdate(t, db)
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

// publishedAsTheHomeWas makes the seeded home's database what the owner's was when it moved
// to PostgreSQL 18: a machine's copy published (cluster_copy_publication, slot 0147, which
// makes the tables without a primary key FULL) and, with keyed, every table with a primary
// key at REPLICA IDENTITY FULL, transcript_segments among them. The publication's name.
func publishedAsTheHomeWas(t *testing.T, db *sql.DB, keyed bool) string {
	t.Helper()
	var pub string
	if err := db.QueryRow(`SELECT cluster_copy_publication('00000000-0000-0000-0000-0000000c0b1e'::uuid)`).Scan(&pub); err != nil {
		t.Fatalf("a machine's publication: %v", err)
	}
	if !keyed {
		return pub
	}
	if _, err := db.Exec(`DO $$ DECLARE t regclass; BEGIN
	    FOR t IN SELECT c.oid::regclass FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
	              WHERE n.nspname = 'public' AND c.relkind = 'r'
	                AND EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.oid AND i.indisprimary) LOOP
	        EXECUTE format('ALTER TABLE %s REPLICA IDENTITY FULL', t);
	    END LOOP; END $$`); err != nil {
		t.Fatalf("the tables with a primary key at REPLICA IDENTITY FULL: %v", err)
	}
	return pub
}

// identitiesAfterTheUpdate: on the updated home no table with a primary key is at REPLICA
// IDENTITY FULL (slot 0238), and the published ones without a primary key still are (the
// home could not update them otherwise).
func identitiesAfterTheUpdate(t *testing.T, db *sql.DB) {
	t.Helper()
	var full, keyless int
	if err := db.QueryRow(`SELECT count(*) FILTER (WHERE has_key), count(*) FILTER (WHERE NOT has_key)
	    FROM (SELECT EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.oid AND i.indisprimary) AS has_key
	            FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
	           WHERE n.nspname = 'public' AND c.relkind = 'r' AND c.relreplident = 'f') x`).Scan(&full, &keyless); err != nil {
		t.Fatalf("the tables' replica identities: %v", err)
	}
	if full > 0 {
		t.Errorf("%d tables with a primary key are still at REPLICA IDENTITY FULL after the update (slot 0238)", full)
	} else {
		t.Logf("after the update: %d tables without a primary key at FULL, none with one", keyless)
	}
	var published int
	if err := db.QueryRow(`SELECT count(*) FROM pg_publication_rel r JOIN pg_class c ON c.oid = r.prrelid
	    WHERE NOT EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.oid AND i.indisprimary)
	      AND c.relreplident <> 'f'`).Scan(&published); err != nil || published > 0 {
		t.Errorf("%d published tables without a primary key lost REPLICA IDENTITY FULL (%v)", published, err)
	}
}

// secondListenStores runs, on the updated home, the second_listen stage's own writes to
// transcript_segments (its registered statements, plugins/first-party/transcription/queries)
// and a reviewer's fix (edits.carry_over), each in a transaction rolled back: PostgreSQL 18
// refuses them while the table's replica identity holds a generated column the publication
// does not send (slot 0237). The table stays published, without search_tsv.
func secondListenStores(t *testing.T, db *sql.DB, pub string) {
	t.Helper()
	var ident string
	if err := db.QueryRow(`SELECT relreplident::text FROM pg_class WHERE oid = 'transcript_segments'::regclass`).Scan(&ident); err != nil || ident != "d" {
		t.Errorf("transcript_segments' replica identity after the update is %q (%v), want d (its primary key)", ident, err)
	}
	var tid string
	if err := db.QueryRow(`SELECT transcript_id::text FROM transcript_segments LIMIT 1`).Scan(&tid); err != nil {
		t.Fatalf("a seeded transcript: %v", err)
	}
	for _, name := range []string{"second_listen.store.4", "second_listen.store.6", "edits.carry_over.4"} {
		args := []any{tid}
		if name == "edits.carry_over.4" {
			args = []any{tid, 0, "text", nil}
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(registeredStatement(t, name), args...); err != nil {
			t.Errorf("%s on the home on PostgreSQL 18 with a machine's copy published: %v", name, err)
		}
		_ = tx.Rollback()
	}
	var cols string
	if err := db.QueryRow(`SELECT attnames::text FROM pg_publication_tables WHERE pubname = $1 AND tablename = 'transcript_segments'`,
		pub).Scan(&cols); err != nil {
		t.Errorf("transcript_segments left the publication %s: %v", pub, err)
	} else if strings.Contains(cols, "search_tsv") || !strings.Contains(cols, "text") {
		t.Errorf("the publication's columns of transcript_segments are %s", cols)
	}
}

// registeredStatement is the SQL of a first-party transcription statement as registered
// (queries/<name>.sql, its header comments left out).
func registeredStatement(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "plugins", "first-party", "transcription", "queries", name+".sql"))
	if err != nil {
		t.Fatalf("the statement %s: %v", name, err)
	}
	var body []string
	for _, l := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(l, "--") {
			body = append(body, l)
		}
	}
	q := strings.TrimSpace(strings.Join(body, "\n"))
	if q == "" {
		t.Fatalf("the statement %s is empty", name)
	}
	return q
}
