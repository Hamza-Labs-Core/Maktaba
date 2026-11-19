//go:build integration && !windows

package pgbundle

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/testport"
)

// TestRealMajorUpgrade (Epic 49, story 49.2) on real programs: a bundled PostgreSQL 16
// cluster (MAKTABA_TEST_PG16_HOME, with pgvector: a payload's postgres-16/ or a release's
// postgres/) with a home's kind of data (pgvector and its HNSW index, full-text search, a
// machine's logical slot) is upgraded to PostgreSQL 18 (MAKTABA_TEST_PG18_HOME) in a
// directory of its own; the app role connects to it with its old password and pg_textsearch
// is there. The 16 cluster still starts with its data (a rollback's release finds it as it
// was). Once 16 moved on (the release before ran on it again), the next upgrade sets the
// stale copy aside and takes what was written meanwhile; and a 16 cluster that was not shut
// down cleanly is recovered first.
func TestRealMajorUpgrade(t *testing.T) {
	h16, h18 := os.Getenv("MAKTABA_TEST_PG16_HOME"), os.Getenv("MAKTABA_TEST_PG18_HOME")
	if h16 == "" || h18 == "" {
		t.Skip("MAKTABA_TEST_PG16_HOME and MAKTABA_TEST_PG18_HOME (PostgreSQL 16 with pgvector, 18 with pgvector and pg_textsearch)")
	}
	if os.Geteuid() == 0 {
		t.Skip("postgres refuses to run as root")
	}
	root, err := os.MkdirTemp("", "mkmaj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	keys := filepath.Join(root, "keys")
	port := testport.Free(t)
	old := Bundle{Home: h16, Data: DataDirFor(root, "16"), Keys: keys, Backups: filepath.Join(root, "backups"), Port: port}
	nw := old
	nw.Home, nw.Data = h18, DataDirFor(root, "18")
	if old.Data != filepath.Join(root, "postgres") || nw.Data != filepath.Join(root, "postgres-18") {
		t.Fatalf("DataDirFor: %s %s", old.Data, nw.Data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	q := func(b Bundle, db, sql string) []string {
		t.Helper()
		qq, err := b.NewQuerier()
		if err != nil {
			t.Fatal(err)
		}
		rows, err := qq.Query(ctx, db, sql)
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return rows
	}
	with := func(b Bundle, fn func()) {
		t.Helper()
		if err := b.withServer(ctx, io.Discard, func() error { fn(); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	with(old, func() {
		q(old, AppDatabase, `CREATE TABLE seg (id int PRIMARY KEY, body text, tsv tsvector
			GENERATED ALWAYS AS (to_tsvector('simple', body)) STORED, emb vector(3));
		INSERT INTO seg (id, body, emb) SELECT g, 'الصبر ' || g, ARRAY[g, 1, 0]::vector FROM generate_series(1, 2000) g;
		CREATE INDEX ON seg USING gin (tsv); CREATE INDEX ON seg USING hnsw (emb vector_cosine_ops);
		CREATE PUBLICATION maktaba_pub FOR TABLE seg;
		SELECT 1 FROM pg_create_logical_replication_slot('machine_a', 'pgoutput');`)
	})
	u := MajorUpgrade{Old: old, New: nw}
	if need, why, err := u.Needed(ctx); err != nil || !need {
		t.Fatalf("Needed = %v %q %v", need, why, err)
	}
	var ulog strings.Builder
	if err := u.Run(ctx, &ulog); err != nil {
		t.Fatalf("%v\n%s", err, ulog.String())
	}
	t.Logf("the upgrade:\n%s", ulog.String())
	if m, err := nw.ClusterMajor(); err != nil || m != "18" {
		t.Fatalf("the new cluster's major: %q %v", m, err)
	}
	if mk, ok, err := ReadUpgradedFrom(nw.Data); err != nil || !ok || mk.Major != "16" || mk.SystemID == "" {
		t.Fatalf("the marker: %+v %v %v", mk, ok, err)
	}
	if err := nw.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := nw.AfterUpgrade(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	with(nw, func() {
		if got := q(nw, AppDatabase, "SELECT count(*) FROM seg WHERE tsv @@ 'الصبر'::tsquery"); got[0] != "2000" {
			t.Errorf("the upgraded rows: %v", got)
		}
		q(nw, AppDatabase, "CREATE EXTENSION IF NOT EXISTS pg_textsearch")
		// clusterSQL ran on 18's own signatures (pg_create_logical_replication_slot has a
		// failover argument since 17): the app role makes the machines' slots
		if got := q(nw, AppDatabase, `SELECT bool_and(has_function_privilege('maktaba', p.oid, 'EXECUTE'))::text FROM pg_proc p WHERE p.proname = 'pg_create_logical_replication_slot'`); got[0] != "true" {
			t.Errorf("the app role's slot grants on 18: %v", got)
		}
		if got := q(nw, AppDatabase, "SELECT current_setting('server_version_num')::int / 10000"); got[0] != "18" {
			t.Errorf("server version %v", got)
		}
		// the app role, with the password every child has
		u, err := nw.URL()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(u, AppRole) {
			t.Fatal(u)
		}
		if err := Ready(ctx, nw.Addr(), AppRole, AppDatabase); err != nil {
			t.Errorf("the app role on the upgraded cluster: %v", err)
		}
	})
	if need, why, err := u.Needed(ctx); err != nil || need {
		t.Fatalf("after the upgrade Needed = %v %q %v", need, why, err)
	}
	// the rollback: the release before starts on its own cluster, as it was
	with(old, func() {
		if got := q(old, AppDatabase, "SELECT count(*) FROM seg"); got[0] != "2000" {
			t.Errorf("the 16 cluster after the upgrade: %v", got)
		}
		if got := q(old, AppDatabase, `SELECT bool_and(has_function_privilege('maktaba', p.oid, 'EXECUTE'))::text FROM pg_proc p WHERE p.proname = 'pg_create_logical_replication_slot'`); got[0] != "true" {
			t.Errorf("the app role's slot grants are not back on 16: %v", got)
		}
		if _, err := os.Stat(filepath.Join(old.Data, clusterMarker)); err != nil {
			t.Errorf("16's cluster marker is not back: %v", err)
		}
		q(old, AppDatabase, "INSERT INTO seg (id, body) VALUES (5000, 'written after the rollback')")
	})
	if need, why, err := u.Needed(ctx); err != nil || !need || !strings.Contains(why, "moved on") {
		t.Fatalf("after the release before wrote: Needed = %v %q %v", need, why, err)
	}
	// the 16 cluster stopped hard (a power cut): recovered, then upgraded again
	srv, err := old.serverCommand(old.serverArgs(false))
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for Ready(ctx, old.Addr(), AppRole, AppDatabase) != nil && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	_ = srv.Process.Kill()
	_ = srv.Wait()
	if err := u.Run(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(nw.Data + ".stale"); err != nil {
		t.Errorf("the stale copy was not set aside: %v", err)
	}
	if err := nw.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	with(nw, func() {
		if got := q(nw, AppDatabase, "SELECT body FROM seg WHERE id = 5000"); len(got) != 1 {
			t.Errorf("the row written after the rollback is not in the new copy: %v", got)
		}
	})
}
