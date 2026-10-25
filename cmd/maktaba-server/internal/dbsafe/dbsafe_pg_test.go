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
	migrate := pgtest.Migrator(t, dir)
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
		api: filepath.Join(dir, "maktaba-api"), repoMg: filepath.Join(pgtest.RepoRoot(t), "shared", "db", "migrations")}
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
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(dump); st.Mode().Perm() != 0o600 {
			t.Fatalf("dump mode %v", st.Mode().Perm())
		}
	}
	bad := filepath.Join(r.t.Backups, "bad.dump")
	raw, _ := os.ReadFile(dump)
	_ = os.WriteFile(bad, raw[:len(raw)/3], 0o600)
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
	// a dump cut part way: its restore fails (before, the schema was dropped first and
	// the database left empty)
	raw, _ := os.ReadFile(dump)
	bad := filepath.Join(r.t.Backups, "bad.dump")
	_ = os.WriteFile(bad, raw[:len(raw)*2/3], 0o600)
	var log bytes.Buffer
	if err := r.t.Restore(ctx, bad, &log); err == nil {
		t.Fatal("a cut dump restored")
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
