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
	script := "#!/bin/sh\n[ \"$1\" = -l ] && exec '" + r.t.bin("pg_restore") + "' \"$@\"\n'" + r.t.bin("pg_restore") + "' --no-owner --no-publications --section=pre-data --file=- \"$DBSAFE_DUMP\"\n" +
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
	if !r.has(t, "videos") {
		t.Fatal("the library's tables are gone")
	}
	var log bytes.Buffer
	if err := r.t.Restore(ctx, dump, &log); err != nil { // whole, it is committed
		t.Fatalf("%v\n%s", err, log.String())
	}
	db2, err := r.t.open(ctx, "") // the restore ended the other sessions
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if err := db2.QueryRow(`SELECT count(*) FROM app_settings WHERE key = 'after.update'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("not restored: %d %v", n, err)
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
