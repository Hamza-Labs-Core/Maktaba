package dbsafe

import (
	"bytes"
	"context"
	"database/sql"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgtest"
)

// pgRig is a bundled Postgres migrated by this tree's maktaba-api (MAKTABA_TEST_PG_BIN).
type pgRig struct {
	t      Target
	b      pgbundle.Bundle
	db     *sql.DB
	api    string
	repoMg string
}

func newPGRig(t *testing.T) *pgRig {
	t.Helper()
	programs := pgtest.Programs(t)
	ctx := context.Background()
	dir := t.TempDir()
	b, _ := pgtest.Bundle(ctx, t, programs, dir, false)
	migrate := pgtest.Migrator(t)
	appURL, err := b.URL()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, appURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	super, err := b.SuperURL()
	if err != nil {
		t.Fatal(err)
	}
	r := &pgRig{t: Target{URL: super, Bin: b.Bin, Backups: filepath.Join(dir, "backups")}, b: b,
		api: pgtest.APIBinary(t), repoMg: filepath.Join(pgtest.RepoRoot(t), "shared", "db", "migrations")}
	if runtime.GOOS == "windows" {
		r.api += ".exe"
	}
	if r.db, err = r.t.open(ctx, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Close() })
	return r
}

func (r *pgRig) has(t *testing.T, table string) bool {
	t.Helper()
	var name sql.NullString
	if err := r.db.QueryRow(`SELECT to_regclass($1)::text`, table).Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name.Valid
}

func (r *pgRig) scratchLeft(t *testing.T) []string {
	t.Helper()
	rows, err := r.db.Query(`SELECT datname FROM pg_database WHERE datname LIKE 'maktaba\_rehearse\_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		out = append(out, n)
	}
	return out
}

// migrationsWith is the repository's migrations plus extra files, in a new directory.
func (r *pgRig) migrationsWith(t *testing.T, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	ents, err := os.ReadDir(r.repoMg)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(r.repoMg, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644)
	}
	for n, body := range extra {
		_ = os.WriteFile(filepath.Join(dir, n), []byte(body), 0o644)
	}
	return dir
}

// TC1 (story 35.5): the pre-update dump reads back and names every table; a corrupt
// one fails; a dump puts a database back as it was, in place.
func TestDumpVerifiedAndRestoredInPlace(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	if err := r.t.Check(ctx); err != nil {
		t.Fatalf("check: %v", err)
	}
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.t.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	n, err := r.t.VerifyDump(ctx, dump)
	if err != nil || n < 100 {
		t.Fatalf("verify: %d tables, %v", n, err)
	}
	// a directory-format dump: the folder the user's only, its table of contents beside
	// one file per table
	st, err := os.Stat(dump)
	if err != nil || !st.IsDir() {
		t.Fatalf("dump %v: %v", st, err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 {
		t.Fatalf("dump mode %v", st.Mode().Perm())
	}
	bad := filepath.Join(r.t.Backups, "bad.dump")
	_ = os.MkdirAll(bad, 0o700)
	raw, _ := os.ReadFile(filepath.Join(dump, "toc.dat"))
	_ = os.WriteFile(filepath.Join(bad, "toc.dat"), raw[:len(raw)/3], 0o600)
	if _, err := r.t.VerifyDump(ctx, bad); err == nil {
		t.Fatal("a cut dump verified")
	}

	// what a failed release left: a table, a row
	if _, err := r.db.Exec(`CREATE TABLE after_update (id int)`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO app_settings (key, value) VALUES ('after.update', '1')`); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := r.t.Restore(ctx, dump, &log); err != nil {
		t.Fatalf("restore: %v\n%s", err, log.String())
	}
	db2, err := r.t.open(ctx, "") // the restore ended every other session
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	r.db = db2
	if r.has(t, "after_update") || !r.has(t, "videos") {
		t.Fatal("the database is not as the dump left it")
	}
	var n2 int
	_ = db2.QueryRow(`SELECT count(*) FROM app_settings WHERE key = 'after.update'`).Scan(&n2)
	if n2 != 0 {
		t.Fatal("a row written after the dump survived the restore")
	}
}

// Story 35.5 with Epic 37: a home whose machines keep copies of its database is put back
// from a dump, in place and bundled. Their slots go (DROP DATABASE refuses to outlive
// them, and they would stream what the dump replaced), so each machine copies the
// restored database anew; the dump's publications do not collide with those still there.
// The review of #433: a database a release before 2026-10-28 put back lost the workers'
// CONNECT (a dump never holds a database's grants); a start gives it back, once.
func TestGrantWorkersConnect(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	has := func() bool {
		var ok bool
		if err := r.db.QueryRow(`SELECT coalesce(datacl::text, '') LIKE '%maktaba_worker=c/%' FROM pg_database
			WHERE datname = current_database()`).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if _, err := r.db.Exec(`DO $$ BEGIN EXECUTE format('REVOKE CONNECT ON DATABASE %I FROM maktaba_worker',
		current_database()); END $$`); err != nil {
		t.Fatal(err)
	}
	if has() {
		t.Fatal("the workers' CONNECT is still there")
	}
	for i := 0; i < 2; i++ {
		if err := r.t.GrantWorkersConnect(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if !has() {
		t.Fatal("the workers' CONNECT is not back")
	}
}

func TestRestoreWithMachineCopies(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	const node = "6f1c2a4e-0b7d-4c55-9e3a-1d2f3a4b5c6d"
	reopen := func() {
		t.Helper()
		d, err := r.t.open(ctx, "") // a restore ends every other session
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.Close() })
		r.db = d
	}
	copyOf := func() {
		t.Helper()
		var pub string
		if err := r.db.QueryRow(`SELECT cluster_copy_publication($1::uuid)`, node).Scan(&pub); err != nil {
			t.Fatalf("the machine's publication: %v", err)
		}
		if _, err := r.db.Exec(`SELECT pg_create_logical_replication_slot($1, 'pgoutput')`, pub); err != nil {
			t.Fatalf("the machine's slot: %v", err)
		}
	}
	slots := func() int {
		t.Helper()
		var n int
		if err := r.db.QueryRow(`SELECT count(*) FROM pg_replication_slots WHERE database = current_database()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	copyOf()
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.t.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO app_settings (key, value) VALUES ('after.update', '1')`); err != nil {
		t.Fatal(err)
	}

	// in place: a Postgres the home does not bundle
	var log bytes.Buffer
	if err := r.t.Restore(ctx, dump, &log); err != nil {
		t.Fatalf("restore in place: %v\n%s", err, log.String())
	}
	reopen()
	if n := slots(); n != 0 {
		t.Fatalf("%d slot(s) left after the restore in place", n)
	}
	var after, tables int
	_ = r.db.QueryRow(`SELECT count(*) FROM app_settings WHERE key = 'after.update'`).Scan(&after)
	if after != 0 || !r.has(t, "videos") {
		t.Fatal("the database is not as the dump left it")
	}
	// the machine's next grant brings its publication up to date with the restored tables
	copyOf()
	if err := r.db.QueryRow(`SELECT count(*) FROM pg_publication_tables WHERE pubname = 'maktaba_copy_' || replace($1, '-', '')`,
		node).Scan(&tables); err != nil || tables < 100 {
		t.Fatalf("the publication after the restore: %d tables, %v", tables, err)
	}

	// bundled: the database is made afresh (DROP DATABASE)
	log.Reset()
	if err := r.b.RestoreRunning(ctx, dump, &log); err != nil {
		t.Fatalf("restore bundled: %v\n%s", err, log.String())
	}
	reopen()
	if n := slots(); n != 0 || !r.has(t, "videos") {
		t.Fatalf("after the bundled restore: %d slot(s), videos %v", n, r.has(t, "videos"))
	}
	copyOf()
}

// Review A11: the dump and the rehearsal's scratch copy need room before they start (a
// full disk takes the live Postgres down); an unknown free space passes.
func TestSpaceForTheDatabaseWork(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	tgt := r.t
	tgt.Data = r.b.Data
	tgt.Free = func(string) uint64 { return 1 << 40 }
	if err := tgt.Space(ctx, true, 20); err != nil {
		t.Fatalf("a roomy disk: %v", err)
	}
	tgt.Free = func(string) uint64 { return 1 << 20 }
	if err := tgt.Space(ctx, false, 20); err == nil || !strings.HasPrefix(err.Error(), "disk_space:") {
		t.Fatalf("no room for the dump: %v", err)
	}
	tgt.Free = func(string) uint64 { return 0 }
	if err := tgt.Space(ctx, true, 20); err != nil {
		t.Fatalf("an unknown free space: %v", err)
	}
	// room for the dump, not for the dump and the scratch copy on the same disk
	size, _ := tgt.Size(ctx)
	tgt.Free = func(string) uint64 { return uint64(size) + headroom + 1<<20 }
	if err := tgt.Space(ctx, false, 20); err != nil {
		t.Fatalf("the dump alone: %v", err)
	}
	if err := tgt.Space(ctx, true, 20); err == nil {
		t.Fatal("the scratch copy too passed")
	}
}

// TC3 (story 35.5): a release's migrations run on a scratch copy; the real database is
// untouched and the copy is dropped; one that fails on the data fails the rehearsal.
func TestRehearsalOnAScratchCopy(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	good := r.migrationsWith(t, map[string]string{"9001_rehearse_probe.sql": "-- +goose Up\n" +
		"CREATE TABLE rehearse_probe AS SELECT id FROM users;\n-- +goose Down\nDROP TABLE rehearse_probe;\n"})
	var log bytes.Buffer
	res, err := r.t.Rehearse(ctx, r.api, good, 20, &log)
	if err != nil {
		t.Fatalf("rehearse: %v\n%s", err, log.String())
	}
	if res.Skipped != "" || res.Pending != 1 || res.MigrateSec <= 0 || res.DBBytes <= 0 {
		t.Fatalf("result %+v", res)
	}
	if r.has(t, "rehearse_probe") {
		t.Fatal("the rehearsal migrated the real database")
	}
	if left := r.scratchLeft(t); len(left) != 0 {
		t.Fatalf("scratch databases left: %v", left)
	}

	// a migration that fails on this database's data
	broken := r.migrationsWith(t, map[string]string{"9002_broken.sql": "-- +goose Up\n" +
		"CREATE TABLE broken_probe (v integer NOT NULL);\nINSERT INTO broken_probe SELECT NULL::integer FROM app_settings;\n" +
		"-- +goose Down\nDROP TABLE broken_probe;\n"})
	if _, err := r.db.Exec(`INSERT INTO app_settings (key, value) VALUES ('rehearsal.probe', '1') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	_, err = r.t.Rehearse(ctx, r.api, broken, 20, &log)
	if err == nil || !strings.HasPrefix(err.Error(), "rehearsal_failed:") {
		t.Fatalf("a migration that fails on the data rehearsed: %v", err)
	}
	if r.has(t, "broken_probe") {
		t.Fatal("the failed rehearsal touched the real database")
	}
	if left := r.scratchLeft(t); len(left) != 0 {
		t.Fatalf("scratch databases left after a failure: %v", left)
	}

	// nothing to rehearse: the release adds no migration
	if res, err := r.t.Rehearse(ctx, r.api, r.repoMg, 20, &log); err != nil || res.Skipped == "" {
		t.Fatalf("no pending migrations: %+v %v", res, err)
	}
}

// TC4 (story 35.5): skipped above rehearse_max_db_gb, and for a role that may not
// create databases (logged, not a failure); a crash's leftover scratch is dropped.
func TestRehearsalSkipsAndLeftovers(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	mig := r.migrationsWith(t, map[string]string{"9001_probe.sql": "-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n"})
	if res, err := r.t.rehearse(ctx, r.api, mig, 1, nil); err != nil || !strings.Contains(res.Skipped, "rehearse_max_db_gb") {
		t.Fatalf("above the size: %+v %v", res, err)
	}
	if _, err := r.db.Exec(`CREATE ROLE no_createdb LOGIN PASSWORD 'pw-no-createdb'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`GRANT CONNECT ON DATABASE ` + pgbundle.AppDatabase + ` TO no_createdb`); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(r.t.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("no_createdb", "pw-no-createdb")
	limited := r.t
	limited.URL = u.String()
	if res, err := limited.Rehearse(ctx, r.api, mig, 20, nil); err != nil || !strings.Contains(res.Skipped, "create databases") {
		t.Fatalf("without CREATEDB: %+v %v", res, err)
	}
	if _, err := r.db.Exec(`CREATE DATABASE maktaba_rehearse_dead`); err != nil {
		t.Fatal(err)
	}
	if err := r.t.DropScratch(ctx); err != nil {
		t.Fatal(err)
	}
	if left := r.scratchLeft(t); len(left) != 0 {
		t.Fatalf("left: %v", left)
	}
}

// Review round 2 (A-P): an update's migrations never wait on another session's lock for
// ever: with --lock-timeout a migration behind a held lock fails fast (and the database
// step tries again), and EndSessions ends the session holding it, as the bundled path
// (psql as the superuser) and as the target's role.
func TestEndSessionsAndTheLockTimeout(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	appURL, err := r.b.URL()
	if err != nil {
		t.Fatal(err)
	}
	holder, err := sql.Open("postgres", appURL)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	hold := func() *sql.Tx {
		t.Helper()
		tx, err := holder.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`LOCK TABLE users IN ACCESS EXCLUSIVE MODE`); err != nil {
			t.Fatal(err)
		}
		return tx
	}
	dir := r.migrationsWith(t, map[string]string{"9003_lock_probe.sql": "-- +goose Up\n" +
		"ALTER TABLE users ADD COLUMN lock_probe integer;\n-- +goose Down\nALTER TABLE users DROP COLUMN lock_probe;\n"})
	migrate := func() (string, error) {
		out, err := runAPI(ctx, r.api, "migrate", "--dir", dir, "--dsn", appURL, "--timeout", "1m", "--lock-timeout", "1s", "up")
		return out, err
	}

	tx := hold()
	if out, err := migrate(); err == nil || !strings.Contains(out, "lock timeout") {
		t.Fatalf("a migration behind a held lock: %v\n%s", err, out)
	}
	n, err := r.b.EndSessions(ctx)
	if err != nil || n < 1 {
		t.Fatalf("bundled: ended %d (%v)", n, err)
	}
	_ = tx.Rollback()
	if out, err := migrate(); err != nil {
		t.Fatalf("with the session ended: %v\n%s", err, out)
	}
	if !r.has(t, "users") {
		t.Fatal("users gone")
	}

	// the target's role (the superuser here) ends them too; replication is left alone
	tx = hold()
	if n, err := r.t.EndSessions(ctx); err != nil || n < 1 {
		t.Fatalf("target: ended %d (%v)", n, err)
	}
	if _, err := tx.Exec(`SELECT 1`); err == nil {
		t.Fatal("the ended session still answers")
	}
	_ = tx.Rollback()
}

// runAPI runs the api binary and returns its output.
func runAPI(ctx context.Context, api string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, api, args...).CombinedOutput()
	return string(out), err
}

// L4 (audit): a restore that fails part way leaves the database as it was, not empty:
// the schema's drop and the dump's restore are one transaction.
func TestRestoreFailingLeavesTheDatabase(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	if !havePSQL(r.t.bin("psql")) {
		t.Skip("no psql beside the Postgres programs")
	}
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.t.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO app_settings (key, value) VALUES ('after.update', '1')`); err != nil {
		t.Fatal(err)
	}
	// a dump cut part way (a table's file of a directory-format one, a single file of
	// before): its restore fails (before, the schema was dropped first and the database
	// left empty)
	bad := filepath.Join(r.t.Backups, "bad.dump")
	cutDump(t, dump, bad)
	old := filepath.Join(r.t.Backups, "old.dump")
	r.oldDump(t, old)
	raw, _ := os.ReadFile(old)
	badOld := filepath.Join(r.t.Backups, "bad-old.dump")
	_ = os.WriteFile(badOld, raw[:len(raw)*2/3], 0o600)
	// a dump whose rows' counts are cut: refused before anything changes
	badCounts := filepath.Join(r.t.Backups, "bad-counts.dump")
	if raw, err := os.ReadFile(filepath.Join(dump, pgbundle.CountsFile)); err != nil {
		t.Fatalf("the dump has no counts: %v", err)
	} else {
		if err := os.MkdirAll(badCounts, 0o700); err != nil {
			t.Fatal(err)
		}
		ents, _ := os.ReadDir(dump)
		for _, e := range ents {
			b, _ := os.ReadFile(filepath.Join(dump, e.Name()))
			_ = os.WriteFile(filepath.Join(badCounts, e.Name()), b, 0o600)
		}
		_ = os.WriteFile(filepath.Join(badCounts, pgbundle.CountsFile), raw[:len(raw)*2/3], 0o600)
	}
	var log bytes.Buffer
	if err := r.t.Restore(ctx, badCounts, &log); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("a dump with damaged counts = %v", err)
	}
	for _, d := range []string{bad, badOld} {
		if err := r.t.Restore(ctx, d, &log); err == nil {
			t.Fatalf("a cut dump restored: %s", d)
		}
	}
	db2, err := r.t.open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	r.db = db2
	var n int
	if err := db2.QueryRow(`SELECT count(*) FROM app_settings WHERE key = 'after.update'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the database changed under a failed restore: %d %v\n%s", n, err, log.String())
	}
	if !r.has(t, "videos") {
		t.Fatal("the failed restore left the database empty")
	}
}

// cutDump copies the directory-format dump src to dst with its largest table's file cut
// to two thirds.
func cutDump(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	var largest string
	var size int64
	for _, e := range ents {
		raw, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		// a table's file: not the table of contents, nor the rows' counts (counts.json,
		// which a small database's dump can make its largest file)
		if e.Name() != "toc.dat" && e.Name() != pgbundle.CountsFile && int64(len(raw)) > size {
			largest, size = e.Name(), int64(len(raw))
		}
		_ = os.WriteFile(filepath.Join(dst, e.Name()), raw, 0o600)
	}
	if largest == "" {
		t.Fatal("no table in the dump")
	}
	if err := os.Truncate(filepath.Join(dst, largest), size*2/3); err != nil {
		t.Fatal(err)
	}
}

// oldDump writes a dump as Maktaba wrote them before the directory format: one
// custom-format file (pg_dump -Fc -Z 1).
func (r *pgRig) oldDump(t *testing.T, path string) {
	t.Helper()
	cmd, err := r.t.command(context.Background(), "pg_dump", "", "--format=custom", "-Z", "1", "--no-owner", "--file="+path)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pg_dump -Fc: %v: %s", err, out)
	}
}

// A dump of before (one custom-format file) is still read and put back in place, as the
// directory-format ones are.
func TestOldSingleFileDumpStillRestores(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	old := filepath.Join(r.t.Backups, "pre-1.3.0-to-1.4.0.dump")
	_ = os.MkdirAll(r.t.Backups, 0o700)
	r.oldDump(t, old)
	if n, err := r.t.VerifyDump(ctx, old); err != nil || n < 100 {
		t.Fatalf("verify: %d tables, %v", n, err)
	}
	if _, err := r.db.Exec(`CREATE TABLE after_update (id int)`); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := r.t.Restore(ctx, old, &log); err != nil {
		t.Fatalf("restore: %v\n%s", err, log.String())
	}
	db2, err := r.t.open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	r.db = db2
	if r.has(t, "after_update") || !r.has(t, "videos") {
		t.Fatal("the database is not as the old dump left it")
	}
}

// Every dump keeps semantic search's vectors: written apart in binary chunks in the dump's
// snapshot (pgbundle.Chunked), not by pg_dump, and put back between the tables and their
// indexes, in place in one transaction and into another database in parallel.
func TestDumpKeepsTheVectorsInChunks(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	if !r.has(t, "segment_embeddings") {
		t.Skip("no pgvector in this Postgres: no segment_embeddings")
	}
	if _, err := r.db.Exec(`
INSERT INTO libraries (id, name, roots) VALUES ('00000000-0000-0000-0000-0000000000a1', 'L', ARRAY['/l']);
INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime) VALUES
  ('00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000a1', repeat('a', 64), '/l/v.mp4', 'v.mp4', 1, now());
INSERT INTO audio_tracks (id, video_id, track_index) VALUES (1, '00000000-0000-0000-0000-0000000000b1', 0);
INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model) VALUES
  ('00000000-0000-0000-0000-0000000000c1', '00000000-0000-0000-0000-0000000000b1', 1, 'en', 'b', 'm');
INSERT INTO transcript_segments (id, transcript_id, seq, start_sec, end_sec, text)
  SELECT g, '00000000-0000-0000-0000-0000000000c1', g, g, g + 1, 'hello ' || g FROM generate_series(1, 300) g;
INSERT INTO segment_embeddings (segment_id, video_id, library_id, language, model, embedding)
  SELECT g, '00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000a1', 'en', 'm',
   ('[' || array_to_string(array_fill((g / 1000.0)::real, ARRAY[384]), ',') || ']')::halfvec FROM generate_series(1, 300) g;`); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	sum := func() string {
		var s string
		if err := r.db.QueryRow(`SELECT count(*) || ':' || sum(segment_id) || ':' || md5(string_agg(embedding::text, ',' ORDER BY segment_id))
			FROM segment_embeddings`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	want := sum()
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.t.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	chunks := pgbundle.Chunks(dump)
	if len(chunks["public.segment_embeddings"]) != pgbundle.DumpJobs() {
		t.Fatalf("chunks %v, want %d of the vectors", chunks, pgbundle.DumpJobs())
	}
	// written after the dump began: not in it
	if _, err := r.db.Exec(`DELETE FROM segment_embeddings WHERE segment_id > 250`); err != nil {
		t.Fatal(err)
	}
	reopen := func() {
		db, err := r.t.open(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		r.db = db
	}
	var log bytes.Buffer
	if err := r.t.Restore(ctx, dump, &log); err != nil {
		t.Fatalf("restore: %v\n%s", err, log.String())
	}
	reopen()
	if got := sum(); got != want || !r.has(t, "segment_embeddings_hnsw") {
		t.Fatalf("in place: vectors %s, want %s (index there: %v)", got, want, r.has(t, "segment_embeddings_hnsw"))
	}
	// into another database (a rehearsal's), in parallel
	if _, err := r.db.Exec(`CREATE DATABASE chunk_check TEMPLATE template0`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = r.db.Exec(`DROP DATABASE IF EXISTS chunk_check WITH (FORCE)`) })
	if err := r.t.restoreInto(ctx, "chunk_check", dump); err != nil {
		t.Fatal(err)
	}
	other, err := r.t.open(ctx, "chunk_check")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var n int
	if err := other.QueryRow(`SELECT count(*) FROM segment_embeddings`).Scan(&n); err != nil || n != 300 {
		t.Fatalf("another database: %d vectors, %v", n, err)
	}
}

// Story 35.5 AC8: a dump says the schema it holds (the newest migration applied in it),
// read from the file alone; a restore by hand runs the kept release of that schema.
func TestDumpSchema(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.t.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	var want int64
	if err := r.db.QueryRow(`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied`).Scan(&want); err != nil {
		t.Fatal(err)
	}
	got, err := r.t.DumpSchema(ctx, dump)
	if err != nil || got != want || got < 100 {
		t.Fatalf("schema %d (%v), want %d", got, err, want)
	}
	bad := filepath.Join(r.t.Backups, "bad.dump")
	_ = os.WriteFile(bad, []byte("not a dump"), 0o600)
	if _, err := r.t.DumpSchema(ctx, bad); err == nil || !strings.Contains(err.Error(), "dump_unreadable") {
		t.Fatalf("not a dump: %v", err)
	}
}

// A server killed (kill -9) in the middle of a restore commits nothing: psql's input ends
// before the script's own COMMIT. (With psql's --single-transaction, which commits at the
// end of its input wherever that is, the part it had read was committed.)
func TestRestoreKilledPartWayCommitsNothing(t *testing.T) {
	if os.Getenv("DBSAFE_RESTORE") != "" { // the server that is killed
		tg := Target{URL: os.Getenv("DBSAFE_RESTORE"), Bin: func(name string) string {
			return filepath.Join(os.Getenv("DBSAFE_BIN"), name)
		}}
		t.Fatalf("the restore ended: %v", tg.Restore(context.Background(), os.Getenv("DBSAFE_DUMP"), os.Stderr))
	}
	r := newPGRig(t)
	if runtime.GOOS == "windows" || !havePSQL(r.t.bin("psql")) {
		t.Skip("needs psql and a shell")
	}
	ctx := context.Background()
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.t.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO app_settings (key, value) VALUES ('after.update', '1')`); err != nil {
		t.Fatal(err)
	}
	before, schema, err := r.t.Identity(ctx)
	if err != nil || before == "" || schema == 0 {
		t.Fatalf("identity %q %d %v", before, schema, err)
	}
	// the programs, with a pg_restore that writes the tables and then hangs (until the server
	// that runs it is gone)
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"psql", "pg_dump"} {
		if err := os.Symlink(r.t.bin(n), filepath.Join(bin, n)); err != nil {
			t.Fatal(err)
		}
	}
	mark := filepath.Join(bin, "wrote")
	script := "#!/bin/sh\n[ \"$1\" = -l ] && exec '" + r.t.bin("pg_restore") + "' \"$@\"\n" +
		"for a; do shift; [ \"$a\" = --section=data ] || set -- \"$@\" \"$a\"; done\n'" + r.t.bin("pg_restore") + "' \"$@\"\n" +
		"echo \"SELECT pg_notify('x', 'y');\"\n: > '" + mark + "'\nwhile kill -0 $PPID; do sleep 1; done\n"
	if err := os.WriteFile(filepath.Join(bin, "pg_restore"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRestoreKilledPartWayCommitsNothing$")
	cmd.Env = append(os.Environ(), "DBSAFE_RESTORE="+r.t.URL, "DBSAFE_BIN="+bin, "DBSAFE_DUMP="+dump)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	pgtest.WaitFor(t, "the script's tables written", time.Minute, func() bool {
		_, err := os.Stat(mark)
		return err == nil
	})
	time.Sleep(2 * time.Second) // psql ran what it read
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	db, err := r.t.open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	pgtest.WaitFor(t, "psql to end", time.Minute, func() bool {
		var n int
		return db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE application_name = 'psql'`).Scan(&n) == nil && n == 0
	})
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM app_settings WHERE key = 'after.update'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("a killed restore changed the database: %d %v\n%s", n, err, out.String())
	}
	if id, _, err := r.t.Identity(ctx); err != nil || id != before {
		t.Fatalf("identity %q, was %q (%v)", id, before, err)
	}
	// whole, it is another database
	var log bytes.Buffer
	if err := r.t.Restore(ctx, dump, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if id, v, err := r.t.Identity(ctx); err != nil || id == before || id == "" || v != schema {
		t.Fatalf("identity after the restore %q %d (%v)", id, v, err)
	}
}

// A table's rows cut short where pg_restore does not notice (PostgreSQL 18's ends well): its
// COPY's end is missing, so psql takes what comes next for rows. Nothing of it is committed,
// nothing waits for ever, and the restore says why it failed.
func TestRestoreOfRowsCutShortCommitsNothing(t *testing.T) {
	r := newPGRig(t)
	if runtime.GOOS == "windows" || !havePSQL(r.t.bin("psql")) {
		t.Skip("needs psql and a shell")
	}
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.t.Dump(context.Background(), dump); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO app_settings (key, value) VALUES ('after.update', '1')`); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pg_restore"), []byte("#!/bin/sh\ncase \"$*\" in\n"+
		"*-l*) echo '1; 0 1 TABLE DATA public cut maktaba';;\n"+
		"*=pre-data*) echo 'CREATE TABLE public.cut (t text);'; echo '-- Data for Name: cut; Type: TABLE DATA; Schema: public; Owner: -'\n"+
		"echo 'COPY public.cut (t) FROM stdin;'; echo 'a row'; printf 'and half of anot';;\nesac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tg := r.t
	tg.Bin = func(name string) string {
		if name == "pg_restore" {
			return filepath.Join(bin, name)
		}
		return r.t.bin(name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var log bytes.Buffer
	err := tg.Restore(ctx, dump, &log)
	if err == nil || ctx.Err() != nil || !strings.Contains(err.Error(), "cut short") {
		t.Fatalf("the restore: %v (%v)", err, ctx.Err())
	}
	db, err := r.t.open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM app_settings WHERE key = 'after.update'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the database changed: %d %v\n%s", n, err, log.String())
	}
}

// scriptRig is a restore whose pg_restore writes the real script with more at its end, and
// whose psql is followed by after (a shell's: it does not end with it).
func scriptRig(t *testing.T, more, after string) (*pgRig, Target, string) {
	t.Helper()
	r := newPGRig(t)
	if runtime.GOOS == "windows" || !havePSQL(r.t.bin("psql")) {
		t.Skip("needs psql and a shell")
	}
	// (a Postgres of the person's own has no pg_textsearch of the home's: #1671)
	if _, err := r.db.Exec(`DROP EXTENSION IF EXISTS pg_textsearch CASCADE; DROP SCHEMA IF EXISTS textsearch CASCADE`); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.t.Dump(context.Background(), dump); err != nil {
		t.Fatal(err)
	}
	prev := psqlLookEvery
	t.Cleanup(func() { psqlLookEvery = prev })
	psqlLookEvery = 100 * time.Millisecond
	bin := t.TempDir()
	for name, body := range map[string]string{
		"pg_restore": "#!/bin/sh\n'" + r.t.bin("pg_restore") + "' \"$@\" || exit 1\ncase \"$*\" in *=post-data*) " + more + ";; esac\n",
		"psql":       "#!/bin/sh\n'" + r.t.bin("psql") + "' \"$@\"\nrc=$?\n[ \"$1\" = --version ] || { " + after + "; }\nexit $rc\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tg := r.t
	tg.Bin = func(name string) string { return filepath.Join(bin, name) }
	return r, tg, dump
}

func runScript(t *testing.T, tg Target, dump string) ([]string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var log bytes.Buffer
	var steps []string
	err := tg.RestoreWith(ctx, dump, func(s string) error { steps = append(steps, s); return nil }, &log)
	if ctx.Err() != nil {
		t.Fatalf("still running after a minute: %v", err)
	}
	return steps, err
}

// A psql that failed and does not end is not waited for, wherever in its script it failed: at
// its end, or with more of it to come than its pipe takes.
func TestRestoreDoesNotWaitForAPsqlThatFailed(t *testing.T) {
	const never = "while kill -0 $PPID; do sleep 1; done"
	for name, more := range map[string]string{
		"at the end": "echo 'SELECT nothing FROM nowhere;'",
		"with more to come": "echo 'SELECT nothing FROM nowhere;'; i=0; while [ $i -lt 20000 ]; do echo 'SELECT 1; -- padding padding'; " +
			"i=$((i+1)); done",
	} {
		t.Run(name, func(t *testing.T) {
			r, tg, dump := scriptRig(t, more, never)
			steps, err := runScript(t, tg, dump)
			if err == nil || !strings.Contains(err.Error(), "psql failed and did not end (its session: gone)") || len(steps) != 0 {
				t.Fatalf("the restore: %v, told %v", err, steps)
			}
			if !r.has(t, "videos") {
				t.Fatal("the library's tables are gone")
			}
		})
	}
}

// A restore that is slow is left alone: whatever name its connection has (its URL's, a
// pooler's), and whatever another session of its own name does.
func TestRestoreThatIsSlowIsLeftAlone(t *testing.T) {
	const slow = "echo 'SELECT pg_sleep(3);'" // 30 looks, 5 of them would end it
	withName := func(u string) string {
		if strings.Contains(u, "?") {
			return u + "&application_name=maktaba"
		}
		return u + "?application_name=maktaba"
	}
	t.Run("a name of its own in the URL", func(t *testing.T) {
		_, tg, dump := scriptRig(t, slow, "true")
		tg.URL = withName(tg.URL)
		before, _, err := tg.Identity(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if steps, err := runScript(t, tg, dump); err != nil || len(steps) != 1 {
			t.Fatalf("%v, told %v", err, steps)
		}
		if id, _, err := tg.Identity(context.Background()); err != nil || id == before {
			t.Fatalf("not restored: %q %v", id, err)
		}
	})
	t.Run("another session of its name that failed", func(t *testing.T) {
		r, tg, dump := scriptRig(t, slow, "true")
		go func() {
			time.Sleep(time.Second) // after the restore ended the other sessions
			d, err := r.t.open(context.Background(), "")
			if err != nil {
				return
			}
			defer d.Close()
			if c, err := d.Conn(context.Background()); err == nil {
				_, _ = c.ExecContext(context.Background(), "SET application_name = '"+pgbundle.RestoreApp+"'")
				_, _ = c.ExecContext(context.Background(), "BEGIN")
				_, _ = c.ExecContext(context.Background(), "SELECT 1/0")
				time.Sleep(4 * time.Second)
			}
		}()
		if steps, err := runScript(t, tg, dump); err != nil || len(steps) != 1 {
			t.Fatalf("%v, told %v", err, steps)
		}
	})
}

// A session a killed restore left is found under any connection name, ended, and the database
// is then told from what it is.
func TestIdentityEndsWhatAKilledRestoreLeft(t *testing.T) {
	r, tg, _ := scriptRig(t, "true", "true")
	u := tg.URL + "?application_name=maktaba"
	if strings.Contains(tg.URL, "?") {
		u = tg.URL + "&application_name=maktaba"
	}
	d, err := (Target{URL: u}).open(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	before, _, err := r.t.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := d.Begin() // as the script begins
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"SET LOCAL application_name = '" + pgbundle.RestoreApp + "'", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
		if _, err := tx.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if id, _, err := r.t.Identity(context.Background()); err != nil || id != before {
		t.Fatalf("identity %q, was %q (%v)", id, before, err)
	}
	if err := tx.Commit(); err == nil {
		t.Fatal("the session a restore left committed after the identity was told")
	}
}

// The watch says nothing of what it cannot see: no answer, another server, a session it was
// not told of; and what it does see, only at several looks in a row.
func TestWatchSessionSaysNothingOfWhatItCannotSee(t *testing.T) {
	r := newPGRig(t)
	prev := psqlLookEvery
	t.Cleanup(func() { psqlLookEvery = prev })
	psqlLookEvery = 20 * time.Millisecond
	var server string
	if err := r.db.QueryRow(`SELECT extract(epoch FROM pg_postmaster_start_time())::text`).Scan(&server); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		tg          Target
		pid, server string
	}{
		"no answer":             {Target{URL: "postgres://x@127.0.0.1:1/x?sslmode=disable"}, "1", server},
		"another server":        {r.t, "999999", "1.5"},
		"a session not told of": {r.t, "", ""},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c.tg.watchSession(ctx, &psqlSession{pid: c.pid, server: c.server}, func(state string) {
			t.Errorf("%s: said %q", name, state)
		})
		cancel()
	}
	// one that is there, then is not
	conn, err := r.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var pid string
	if err := conn.QueryRowContext(context.Background(), `SELECT pg_backend_pid()::text`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(300*time.Millisecond, func() {
		d, err := r.t.open(context.Background(), "")
		if err == nil {
			_, _ = d.Exec(`SELECT pg_terminate_backend($1::int)`, pid)
			_ = d.Close()
		}
	})
	said := ""
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r.t.watchSession(ctx, &psqlSession{pid: pid, server: server}, func(state string) { said = state })
	if said != "gone" {
		t.Fatalf("a session that went: %q", said)
	}
}

func TestPsqlSessionSaid(t *testing.T) {
	text := "NOTICE:  x\nmaktaba-restore-1-session 4242 1700000000.5\nmaktaba-restore-1-ran\n"
	for _, size := range []int{1, 5, 1 << 20} {
		s := &psqlSession{word: "maktaba-restore-1-session"}
		for rest := text; rest != ""; {
			n := min(size, len(rest))
			_, _ = s.Write([]byte(rest[:n]))
			rest = rest[n:]
		}
		if pid, server := s.said(); pid != "4242" || server != "1700000000.5" {
			t.Errorf("%d bytes at a time: %q %q", size, pid, server)
		}
	}
}

// A home killed while it closed its own database to sessions, for another to take its place,
// left it closed to everyone: it is the same database, and takes sessions again. A person's own
// is left as they have it.
func TestIdentityOpensWhatAKilledRestoreClosed(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	before, schema, err := r.t.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	own, err := r.t.open(ctx, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer own.Close()
	if _, err := own.Exec(`ALTER DATABASE ` + pgbundle.AppDatabase + ` ALLOW_CONNECTIONS false`); err != nil {
		t.Fatal(err)
	}
	if id, _, err := r.t.Identity(ctx); err == nil {
		t.Fatalf("a closed database of a person's own: %q", id)
	}
	bundled := r.t
	bundled.Data = t.TempDir()
	if id, v, err := bundled.Identity(ctx); err != nil || id != before || v != schema {
		t.Fatalf("identity %q %d, was %q %d (%v)", id, v, before, schema, err)
	}
	if _, _, err := r.t.Identity(ctx); err != nil {
		t.Fatalf("still closed: %v", err)
	}
}

// A script that fails part way, or a pg_restore that does, commits nothing and says so, though
// the database it would have checked against is the same (the same rows as the dump's).
func TestRestoreThatFailsPartWayCommitsNothing(t *testing.T) {
	r := newPGRig(t)
	if runtime.GOOS == "windows" || !havePSQL(r.t.bin("psql")) {
		t.Skip("needs psql and a shell")
	}
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.t.Dump(context.Background(), dump); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := r.db.QueryRow(`SELECT oid::text FROM pg_namespace WHERE nspname = 'public'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string][2]string{
		"an SQL error":         {"echo 'SELECT nothing FROM nowhere;'", "nowhere"},
		"pg_restore failing":   {"echo 'pg_restore: error: could not read' >&2; exit 1", "could not read"},
		"pg_restore, no words": {"exit 1", "exit status 1"},
	} {
		bin := t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, "pg_restore"), []byte("#!/bin/sh\n'"+r.t.bin("pg_restore")+"' \"$@\" || exit 1\n"+
			"case \"$*\" in *=post-data*) "+c[0]+";; esac\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		tg := r.t
		tg.Bin = func(n string) string {
			if n == "pg_restore" {
				return filepath.Join(bin, n)
			}
			return r.t.bin(n)
		}
		var log bytes.Buffer
		if err := tg.Restore(context.Background(), dump, &log); err == nil || !strings.Contains(err.Error(), c[1]) {
			t.Errorf("%s: %v", name, err)
		}
		d, err := r.t.open(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		var now string
		if err := d.QueryRow(`SELECT oid::text FROM pg_namespace WHERE nspname = 'public'`).Scan(&now); err != nil || now != before {
			t.Errorf("%s: another schema is there (%v)", name, err)
		}
		_ = d.Close()
	}
}

// A chunked table's chunks that are damaged are refused before anything is touched (the copy
// slots too), with the dump's counts or without; a last chunk that is gone, and any table short
// of its counts, before the commit.
func TestRestoreOfDamagedChunksChangesNothing(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	if !havePSQL(r.t.bin("psql")) {
		t.Skip("needs psql")
	}
	prev := pgbundle.Chunked
	t.Cleanup(func() { pgbundle.Chunked = prev })
	pgbundle.Chunked = []pgbundle.ChunkedTable{{Name: "public.chunk_t", Key: "id"}}
	exec := func(q string) {
		t.Helper()
		d, err := r.t.open(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	one := func(q string) (n int) {
		t.Helper()
		d, err := r.t.open(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		if err := d.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	exec(`CREATE TABLE chunk_t (id bigint PRIMARY KEY, body text); INSERT INTO chunk_t SELECT g, 'row ' || g FROM generate_series(1, 300) g`)
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.t.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	chunks := pgbundle.Chunks(dump)["public.chunk_t"]
	if len(chunks) < 2 {
		t.Skipf("%d chunks", len(chunks))
	}
	exec(`INSERT INTO chunk_t VALUES (1000, 'since')`)
	first, err := os.ReadFile(chunks[0])
	if err != nil {
		t.Fatal(err)
	}
	// the first row's end: 2 bytes of fields, two lengths and their bytes
	row := 19 + 2 + 4 + 8 + 4 + len("row 1")
	counts := filepath.Join(dump, pgbundle.CountsFile)
	whole, err := os.ReadFile(counts)
	if err != nil {
		t.Fatal(err)
	}
	last := chunks[len(chunks)-1]
	for name, c := range map[string]struct {
		do, undo func() error
		noCounts bool // refused with no counts too
		want     string
	}{
		"cut between two rows": {func() error { return os.WriteFile(chunks[0], first[:row], 0o600) },
			func() error { return os.WriteFile(chunks[0], first, 0o600) }, true, "cut short"},
		"cut in a row": {func() error { return os.WriteFile(chunks[0], first[:row-3], 0o600) },
			func() error { return os.WriteFile(chunks[0], first, 0o600) }, true, "cut short"},
		"empty": {func() error { return os.WriteFile(chunks[0], nil, 0o600) },
			func() error { return os.WriteFile(chunks[0], first, 0o600) }, true, "cut short"},
		"one gone": {func() error { return os.Rename(chunks[0], chunks[0]+".away") },
			func() error { return os.Rename(chunks[0]+".away", chunks[0]) }, true, "is not there"},
		"the last gone": {func() error { return os.Rename(last, last+".away") },
			func() error { return os.Rename(last+".away", last) }, false, "rows"},
		"a table short of its counts": {func() error {
			return os.WriteFile(counts, bytes.Replace(whole, []byte(`"public.app_settings":`), []byte(`"public.app_settings":9`), 1), 0o600)
		}, func() error { return os.WriteFile(counts, whole, 0o600) }, false, "public.app_settings"},
	} {
		for _, with := range []bool{true, false} {
			if !with && !c.noCounts {
				continue
			}
			if err := c.do(); err != nil {
				t.Fatal(err)
			}
			if !with {
				if err := os.Remove(counts); err != nil {
					t.Fatal(err)
				}
			}
			var log bytes.Buffer
			if err := r.t.Restore(ctx, dump, &log); err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "nothing changed") {
				t.Errorf("%s, counts %v: %v", name, with, err)
			}
			if n := one(`SELECT count(*) FROM chunk_t WHERE id = 1000`); n != 1 {
				t.Errorf("%s, counts %v: the database changed", name, with)
			}
			if c.want == "cut short" || c.want == "is not there" {
				if err := r.t.restoreInto(ctx, "postgres", dump); err == nil || !strings.Contains(err.Error(), c.want) {
					t.Errorf("%s: a rehearsal's: %v", name, err)
				}
			}
			if err := c.undo(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(counts, whole, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	var log bytes.Buffer
	if err := r.t.Restore(ctx, dump, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if n := one(`SELECT count(*) FROM chunk_t`); n != 300 {
		t.Fatalf("%d rows", n)
	}
}

// A dump that cannot be listed is refused before anything is touched: the machines' copies keep
// their slots.
func TestRestoreOfAnUnreadableDumpTouchesNothing(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	if _, err := r.db.Exec(`SELECT pg_create_logical_replication_slot('mk_kept', 'pgoutput')`); err != nil {
		t.Skipf("no logical slots here: %v", err)
	}
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := os.MkdirAll(dump, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dump, "toc.dat"), []byte("PGDMP cut"), 0o600); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := r.t.Restore(ctx, dump, &log); err == nil || !strings.Contains(err.Error(), "nothing changed") {
		t.Fatal(err)
	}
	var n int
	if err := r.db.QueryRow(`SELECT count(*) FROM pg_replication_slots WHERE slot_name = 'mk_kept'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the slot: %d %v", n, err)
	}
}

// A table whose owner's own INSERT is revoked restores: pg_restore gives the grants after the
// rows.
func TestRestoreGivesTheGrantsAfterTheRows(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	if !havePSQL(r.t.bin("psql")) {
		t.Skip("needs psql")
	}
	u, err := r.b.URL() // the app role, which owns its database and is no superuser
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`CREATE DATABASE grants_check OWNER ` + pgbundle.AppRole + ` TEMPLATE template0`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = r.db.Exec(`DROP DATABASE IF EXISTS grants_check WITH (FORCE)`) })
	tg := r.t
	if tg.URL, err = (Target{URL: u}).urlFor("grants_check"); err != nil {
		t.Fatal(err)
	}
	d, err := tg.open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`CREATE TABLE own (id int); INSERT INTO own VALUES (1); REVOKE INSERT ON own FROM ` + pgbundle.AppRole); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(r.t.Backups, "grants.dump")
	if err := tg.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := tg.Restore(ctx, dump, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
}

// Only public is made afresh: an extension in a schema of its own, made by the superuser
// (pg_textsearch on an operator's own PostgreSQL 18), is there still, and the dump's items that
// make it again, with its schema, grants and comments, are left out. Every role has the use of
// public a new database gives it. With psql and without, for a role that could make none of it.
func TestRestoreLeavesWhatIsOutsidePublic(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	u, err := r.b.URL() // the app role, which owns its database and is no superuser
	if err != nil {
		t.Fatal(err)
	}
	must := func(d *sql.DB, q string) {
		t.Helper()
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	must(r.db, `CREATE DATABASE outside_check OWNER `+pgbundle.AppRole+` TEMPLATE template0`)
	must(r.db, `CREATE ROLE outside_reader NOLOGIN; CREATE ROLE outside_maker NOLOGIN`)
	t.Cleanup(func() {
		d, err := r.t.open(ctx, "")
		if err != nil {
			return
		}
		defer d.Close()
		_, _ = d.Exec(`DROP DATABASE IF EXISTS outside_check WITH (FORCE)`)
		_, _ = d.Exec(`DROP ROLE IF EXISTS outside_reader, outside_maker`)
	})
	super, err := r.t.open(ctx, "outside_check")
	if err != nil {
		t.Fatal(err)
	}
	defer super.Close()
	must(super, `CREATE SCHEMA outside; GRANT USAGE ON SCHEMA outside TO `+pgbundle.AppRole+`;
		CREATE EXTENSION pageinspect SCHEMA outside; COMMENT ON SCHEMA outside IS 'the operator''s';
		GRANT CREATE ON SCHEMA public TO outside_maker`)
	tg := r.t
	if tg.URL, err = (Target{URL: u}).urlFor("outside_check"); err != nil {
		t.Fatal(err)
	}
	app, err := tg.open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	must(app, `CREATE TABLE own (id int); INSERT INTO own VALUES (1), (2)`)
	dump := filepath.Join(r.t.Backups, "outside.dump")
	if err := tg.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	_ = app.Close()
	list, err := tg.list(ctx, dump)
	if err != nil || !strings.Contains(list, " SCHEMA - outside ") || !strings.Contains(list, " COMMENT - EXTENSION pageinspect") {
		t.Fatalf("the dump makes no extension of its own schema, so this shows nothing: %v\n%s", err, list)
	}
	lists := t.TempDir()
	t.Setenv("TMPDIR", lists)
	t.Setenv("TMP", lists) // Windows
	for name, psql := range map[string]bool{"with psql": true, "without psql": false} {
		if psql && !havePSQL(r.t.bin("psql")) {
			continue
		}
		rt := tg
		if !psql {
			rt.Bin = func(n string) string {
				if n == "psql" {
					return filepath.Join(t.TempDir(), "no-such-psql")
				}
				return r.t.bin(n)
			}
		}
		d, err := tg.open(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		must(d, `INSERT INTO own VALUES (3)`) // a row since
		_ = d.Close()
		var log bytes.Buffer
		err = rt.Restore(ctx, dump, &log)
		if !psql {
			if err == nil || !CannotRestore(err) || !strings.Contains(err.Error(), "(nothing changed)") {
				t.Fatalf("%s: want refusal without psql, got %v\n%s", name, err, log.String())
			}
			if d, err = r.t.open(ctx, "outside_check"); err != nil {
				t.Fatal(err)
			}
			var rows int
			if err := d.QueryRow(`SELECT count(*) FROM own`).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			_ = d.Close()
			if rows != 3 {
				t.Errorf("%s: %d rows, want 3 (nothing changed)", name, rows)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, log.String())
		}
		if d, err = r.t.open(ctx, "outside_check"); err != nil {
			t.Fatal(err)
		}
		var rows int
		var there, uses bool
		if err := d.QueryRow(`SELECT (SELECT count(*) FROM own),
			EXISTS (SELECT FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace WHERE e.extname = 'pageinspect' AND n.nspname = 'outside'
				AND has_schema_privilege('`+pgbundle.AppRole+`', n.oid, 'USAGE')),
			has_schema_privilege('outside_reader', 'public', 'USAGE') AND NOT has_schema_privilege('outside_reader', 'public', 'CREATE')
				AND has_schema_privilege('outside_maker', 'public', 'CREATE')`).
			Scan(&rows, &there, &uses); err != nil {
			t.Fatal(err)
		}
		_ = d.Close()
		if rows != 2 || !there || !uses {
			t.Errorf("%s: %d rows, the extension there %v, public's grants %v", name, rows, there, uses)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(lists, "maktaba-restore-list-*")); len(left) != 0 {
		t.Errorf("left %v", left)
	}
}

// With no psql a dump with chunks is refused before anything is touched: no session is ended.
func TestChunksWithoutPsqlTouchNothing(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	prev := pgbundle.Chunked
	t.Cleanup(func() { pgbundle.Chunked = prev })
	pgbundle.Chunked = []pgbundle.ChunkedTable{{Name: "public.chunk_t", Key: "id"}}
	if _, err := r.db.Exec(`CREATE TABLE chunk_t (id int PRIMARY KEY, v text); INSERT INTO chunk_t SELECT i, 'x' FROM generate_series(1, 50) i`); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.t.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	if len(pgbundle.Chunks(dump)) == 0 {
		t.Skip("the dump has no chunks (no psql here)")
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var pid, now int
	if err := conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	tg := r.t
	tg.Bin = func(n string) string {
		if n == "psql" {
			return filepath.Join(t.TempDir(), "no-such-psql")
		}
		return r.t.bin(n)
	}
	if err := tg.Restore(ctx, dump, nil); err == nil || !strings.Contains(err.Error(), "(nothing changed)") || !CannotRestore(err) {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&now); err != nil || now != pid {
		t.Fatalf("the session was ended: %v", err)
	}
}

// An extension in public that the database user may not make again (pgvector, made by a
// superuser): without psql it is refused before anything is touched; with psql the backup is put
// back inside public, and the extension is the one it was. A superuser's is made again.
func TestRestoreOfAnExtensionItMayNotMakeTouchesNothing(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	u, err := r.b.URL()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`CREATE DATABASE ext_check OWNER ` + pgbundle.AppRole + ` TEMPLATE template0`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if d, err := r.t.open(ctx, ""); err == nil {
			_, _ = d.Exec(`DROP DATABASE IF EXISTS ext_check WITH (FORCE)`)
			_ = d.Close()
		}
	})
	super := r.t
	if super.URL, err = r.t.urlFor("ext_check"); err != nil {
		t.Fatal(err)
	}
	sd, err := super.open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer sd.Close()
	if _, err := sd.Exec(`CREATE EXTENSION hstore`); err != nil { // trusted
		t.Fatal(err)
	}
	tg := r.t
	if tg.URL, err = (Target{URL: u}).urlFor("ext_check"); err != nil {
		t.Fatal(err)
	}
	d, err := tg.open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`CREATE TABLE own (id int); INSERT INTO own VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	earlier, dump := filepath.Join(r.t.Backups, "earlier.dump"), filepath.Join(r.t.Backups, "ext.dump")
	if err := tg.Dump(ctx, earlier); err != nil {
		t.Fatal(err)
	}
	if _, err := sd.Exec(`CREATE EXTENSION pageinspect`); err != nil {
		t.Fatal(err)
	}
	if err := tg.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	conn, err := d.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	without := tg
	without.Bin = func(n string) string {
		if n == "psql" {
			return filepath.Join(t.TempDir(), "no-such-psql")
		}
		return r.t.bin(n)
	}
	if err := without.Restore(ctx, dump, nil); err == nil || !strings.Contains(err.Error(), "(nothing changed)") ||
		!strings.Contains(err.Error(), "pageinspect") || strings.Contains(err.Error(), "hstore") {
		t.Fatalf("without psql: %v", err)
	}
	var n int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM own`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the session was ended, or the rows are gone: %d %v", n, err)
	}
	_ = conn.Close()
	if !havePSQL(r.t.bin("psql")) {
		return
	}
	ext := func() (oid string) {
		t.Helper()
		x, err := super.open(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		defer x.Close()
		if err := x.QueryRow(`SELECT oid::text FROM pg_extension WHERE extname = 'pageinspect'`).Scan(&oid); err != nil {
			t.Fatal(err)
		}
		return oid
	}
	was := ext()
	if err := tg.Restore(ctx, dump, nil); err != nil || ext() != was {
		t.Fatalf("with psql: %v, the extension %s, was %s", err, ext(), was)
	}
	if d, err = tg.open(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM own`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("with psql: %d rows, %v", n, err)
	}
	_ = d.Close()
	// without psql, even a backup from before the extension is refused up front (one transaction takes psql)
	if err := without.Restore(ctx, earlier, nil); err == nil || !CannotRestore(err) || !strings.Contains(err.Error(), "psql") {
		t.Fatalf("without psql on earlier backup: %v", err)
	}
	// with psql, a backup from before it was made succeeds
	if err := tg.Restore(ctx, earlier, nil); err != nil {
		t.Fatal(err)
	}
	if err := super.Restore(ctx, dump, nil); err != nil || ext() == was {
		t.Fatalf("a superuser's: %v, the extension %s, was %s", err, ext(), was)
	}
}

// An extension in a schema of its own that depends on one in public goes with public, and is made
// again, not left out.
func TestRestoreMakesWhatGoesWithPublicAgain(t *testing.T) {
	r := newPGRig(t)
	ctx := context.Background()
	if _, err := r.db.Exec(`CREATE EXTENSION cube; CREATE SCHEMA outside; CREATE EXTENSION earthdistance SCHEMA outside`); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(r.t.Backups, "pre-1.4.0-to-1.5.0.dump")
	if err := r.t.Dump(ctx, dump); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := r.t.Restore(ctx, dump, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	d, err := r.t.open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var in string
	if err := d.QueryRow(`SELECT string_agg(n.nspname, ' ' ORDER BY e.extname) FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace
		WHERE e.extname IN ('cube', 'earthdistance')`).Scan(&in); err != nil || in != "public outside" {
		t.Fatalf("in %q, %v", in, err)
	}
}
