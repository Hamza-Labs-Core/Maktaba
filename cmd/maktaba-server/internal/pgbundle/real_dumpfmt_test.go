//go:build integration && !windows

package pgbundle

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// realServer is a scratch cluster of the Postgres install on this machine (realHome),
// prepared as the app prepares its own and running.
func realServer(t *testing.T) (Bundle, Querier) {
	t.Helper()
	home := realHome(t)
	if os.Geteuid() == 0 {
		t.Skip("postgres refuses to run as root")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	root, err := os.MkdirTemp("", "mkpg") // short: the unix socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	b := Bundle{Home: home, Data: filepath.Join(root, "pg"), Keys: filepath.Join(root, "keys"),
		Backups: filepath.Join(root, "backups"), Port: port}
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	srv := exec.Command(b.Bin("postgres"), b.ServerArgs()...)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Signal(os.Interrupt); _ = srv.Wait() })
	deadline := time.Now().Add(30 * time.Second)
	for Ready(ctx, b.Addr(), AppRole, AppDatabase) != nil {
		if time.Now().After(deadline) {
			t.Fatal("not ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	q, err := b.NewQuerier()
	if err != nil {
		t.Fatal(err)
	}
	return b, q
}

// one is the single value sql selects.
func one(t *testing.T, q Querier, sql string) string {
	t.Helper()
	rows, err := q.Query(context.Background(), AppDatabase, sql)
	if err != nil || len(rows) != 1 {
		t.Fatalf("%s = %v, %v", sql, rows, err)
	}
	return rows[0]
}

// The parallel dump and its parallel restore, on a real cluster: a directory-format dump
// (several workers, one file per table) of a database of several tables, put back into
// the running server whole; the pre-update one without the vectors, whose table and index
// come back empty. A dump of before (one custom-format file) is put back the same way.
func TestRealParallelDumpAndRestore(t *testing.T) {
	b, q := realServer(t)
	ctx := context.Background()
	vec := one(t, q, `SELECT EXISTS (SELECT FROM pg_extension WHERE extname = 'vector')`) == "t"
	embedding, index, lit := "real[]", "", "{0.1,0.2,0.3,0.4}"
	if vec {
		embedding, lit, index = "halfvec(4)", "[0.1,0.2,0.3,0.4]", `CREATE INDEX segment_embeddings_hnsw ON segment_embeddings USING hnsw (embedding halfvec_cosine_ops);`
	}
	if err := q.Exec(ctx, AppDatabase, `SET ROLE `+AppRole+`;
CREATE TABLE goose_db_version (id serial PRIMARY KEY, version_id bigint NOT NULL, is_applied boolean NOT NULL);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (163, true);
CREATE TABLE videos (id serial PRIMARY KEY, title text NOT NULL);
CREATE TABLE transcript_segments (id bigserial PRIMARY KEY, video_id int NOT NULL REFERENCES videos(id), text text NOT NULL);
CREATE TABLE events (id bigserial PRIMARY KEY, body text NOT NULL);
CREATE TABLE segment_embeddings (segment_id bigint PRIMARY KEY REFERENCES transcript_segments(id) ON DELETE CASCADE,
  embedding `+embedding+` NOT NULL);
`+index+`
INSERT INTO videos (title) SELECT 'video ' || g FROM generate_series(1, 200) g;
INSERT INTO transcript_segments (video_id, text) SELECT 1 + g % 200, repeat('words ', 20) || g FROM generate_series(1, 20000) g;
INSERT INTO events (body) SELECT md5(g::text) FROM generate_series(1, 20000) g;
INSERT INTO segment_embeddings SELECT id, '`+lit+`' FROM transcript_segments;`); err != nil {
		t.Fatal(err)
	}
	// a machine's copy's publication (Epic 37): not put back (its next grant makes it again)
	if err := q.Exec(ctx, AppDatabase, `CREATE PUBLICATION maktaba_copy_test FOR TABLE events, videos;`); err != nil {
		t.Fatal(err)
	}
	whole, err := b.Backup(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pre, err := b.DumpBeforeUpdate(ctx, "v0.3.35", "v0.3.36", 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{whole, pre} {
		st, err := os.Stat(d)
		if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
			t.Fatalf("%s is no directory-format dump the user's only: %v %v", d, st, err)
		}
		if _, err := os.Stat(filepath.Join(d, "toc.dat")); err != nil {
			t.Fatal(err)
		}
	}
	if DumpBytes(pre) >= DumpBytes(whole) {
		t.Fatalf("the pre-update dump (%d bytes) is not smaller than the whole one (%d)", DumpBytes(pre), DumpBytes(whole))
	}
	// what a failed update did
	if err := q.Exec(ctx, AppDatabase, `SET ROLE `+AppRole+`;
INSERT INTO goose_db_version (version_id, is_applied) VALUES (164, true);
DELETE FROM events WHERE id % 2 = 0;
CREATE TABLE channels (id serial PRIMARY KEY);`); err != nil {
		t.Fatal(err)
	}
	check := func(what string, vectors string) {
		t.Helper()
		for sql, want := range map[string]string{
			`SELECT count(*) FROM videos`:                                 "200",
			`SELECT count(*) FROM transcript_segments`:                    "20000",
			`SELECT count(*) FROM events`:                                 "20000",
			`SELECT count(*) FROM segment_embeddings`:                     vectors,
			`SELECT max(version_id) FROM goose_db_version`:                "163",
			`SELECT to_regclass('channels') IS NULL`:                      "t",
			`SELECT tableowner FROM pg_tables WHERE tablename = 'events'`: AppRole,
			`SELECT count(*) FROM pg_publication`:                         "0",
		} {
			if got := one(t, q, sql); got != want {
				t.Errorf("%s: %s = %s, want %s", what, sql, got, want)
			}
		}
		if vec && one(t, q, `SELECT to_regclass('segment_embeddings_hnsw') IS NOT NULL`) != "t" {
			t.Errorf("%s: the vectors' index is not back", what)
		}
	}
	start := time.Now()
	if err := b.RestoreRunning(ctx, pre, io.Discard); err != nil {
		t.Fatal(err)
	}
	t.Logf("the pre-update dump (%d bytes) put back in %s", DumpBytes(pre), time.Since(start))
	check("the pre-update dump", "0")
	if err := b.RestoreRunning(ctx, whole, io.Discard); err != nil {
		t.Fatal(err)
	}
	check("the whole dump", "20000")

	// a dump of before: one custom-format file, as pg_dump -Fc -Z 1 wrote it
	old := filepath.Join(b.Backups, "pre-v0.3.34-to-v0.3.35.dump")
	cmd := exec.CommandContext(ctx, b.Bin("pg_dump"), "-Fc", "-Z", "1", "-h", "127.0.0.1", "-p", strconv.Itoa(b.Port),
		"-U", SuperUser, "-f", old, AppDatabase)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+q.pw)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pg_dump -Fc: %v: %s", err, out)
	}
	if err := q.Exec(ctx, AppDatabase, `SET ROLE `+AppRole+`; DELETE FROM videos WHERE id > 100 AND NOT EXISTS
  (SELECT FROM transcript_segments s WHERE s.video_id = videos.id); CREATE TABLE channels (id serial PRIMARY KEY);
  UPDATE goose_db_version SET version_id = 164 WHERE version_id = 163;`); err != nil {
		t.Fatal(err)
	}
	if err := b.RestoreRunning(ctx, old, io.Discard); err != nil {
		t.Fatal(err)
	}
	check("a single-file dump of before", "20000")
	// the folder's rules see the three
	ds, err := ListDumps(b.Backups)
	if err != nil || len(ds) != 3 {
		t.Fatalf("dumps %+v, %v", ds, err)
	}
	if err := Ready(ctx, b.Addr(), AppRole, AppDatabase); err != nil {
		t.Fatalf("the app role after the restores: %v", err)
	}
}
