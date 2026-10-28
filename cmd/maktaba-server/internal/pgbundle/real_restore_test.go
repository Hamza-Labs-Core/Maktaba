//go:build integration && !windows

package pgbundle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// outageDatabase makes a home's database as the outage's had it, small: tables with
// their keys, indexes, triggers and foreign keys, grants to the workers' and the
// replicas' groups (table and column ones), default privileges, the database's own
// CONNECT for the workers' group, vectors in a chunked table, and a joined machine's
// login (a member of the workers' group) with what it calls to get its copy: a function
// that grants every table to the replicas' group, as cluster_copy_publication does.
func outageDatabase(t *testing.T, q Querier, tables int) {
	t.Helper()
	ctx := context.Background()
	vec := one(t, q, `SELECT EXISTS (SELECT FROM pg_extension WHERE extname = 'vector')`) == "t"
	embedding, lit, index := "real[]", "{0.1,0.2,0.3,0.4}", ""
	if vec {
		embedding, lit, index = "halfvec(4)", "[0.1,0.2,0.3,0.4]",
			`CREATE INDEX segment_embeddings_hnsw ON segment_embeddings USING hnsw (embedding halfvec_cosine_ops);`
	}
	var sb strings.Builder
	sb.WriteString(`DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'maktaba_worker') THEN CREATE ROLE maktaba_worker NOLOGIN; END IF;
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'maktaba_replicas') THEN CREATE ROLE maktaba_replicas NOLOGIN; END IF;
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'node_login') THEN CREATE ROLE node_login LOGIN PASSWORD 'node-pw'; END IF;
END $$;
GRANT maktaba_worker TO node_login;
GRANT CONNECT ON DATABASE ` + AppDatabase + ` TO maktaba_worker;
SET ROLE ` + AppRole + `;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO maktaba_worker;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE ON SEQUENCES TO maktaba_worker;
CREATE TABLE goose_db_version (id serial PRIMARY KEY, version_id bigint NOT NULL, is_applied boolean NOT NULL);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (167, true);
CREATE TABLE videos (id serial PRIMARY KEY, title text NOT NULL);
INSERT INTO videos (title) SELECT 'video ' || g FROM generate_series(1, 300) g;
CREATE TABLE transcript_segments (id bigserial PRIMARY KEY, video_id int NOT NULL REFERENCES videos(id), text text NOT NULL);
INSERT INTO transcript_segments (video_id, text) SELECT 1 + g % 300, 'words ' || g FROM generate_series(1, 5000) g;
CREATE TABLE segment_embeddings (segment_id bigint PRIMARY KEY REFERENCES transcript_segments(id) ON DELETE CASCADE,
  embedding ` + embedding + ` NOT NULL);
INSERT INTO segment_embeddings SELECT id, '` + lit + `' FROM transcript_segments;
` + index + `
CREATE FUNCTION touch() RETURNS trigger LANGUAGE plpgsql AS $f$ BEGIN RETURN NEW; END $f$;
`)
	for i := 0; i < tables; i++ {
		fmt.Fprintf(&sb, `CREATE TABLE t%03d (id bigserial PRIMARY KEY, video_id int REFERENCES videos(id), note text, n int CHECK (n >= 0));
CREATE INDEX t%03d_note ON t%03d (note);
CREATE TRIGGER t%03d_touch BEFORE UPDATE ON t%03d FOR EACH ROW EXECUTE FUNCTION touch();
INSERT INTO t%03d (video_id, note, n) SELECT 1 + g %% 300, 'n' || g, g FROM generate_series(1, %d) g;
GRANT SELECT, INSERT, UPDATE, DELETE ON t%03d TO maktaba_worker;
GRANT SELECT (id, note) ON t%03d TO maktaba_replicas;
`, i, i, i, i, i, i, 20+i, i, i)
	}
	sb.WriteString(`GRANT SELECT ON videos, transcript_segments, segment_embeddings TO maktaba_worker;
RESET ROLE;
CREATE FUNCTION copy_grant() RETURNS int LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $f$
DECLARE r regclass; n int := 0;
BEGIN
  FOR r IN SELECT c.oid::regclass FROM pg_class c JOIN pg_namespace s ON s.oid = c.relnamespace
            WHERE s.nspname = 'public' AND c.relkind = 'r' LOOP
    EXECUTE format('GRANT SELECT ON %s TO maktaba_replicas', r);
    n := n + 1;
  END LOOP;
  RETURN n;
END $f$;
GRANT EXECUTE ON FUNCTION copy_grant() TO maktaba_worker;
`)
	if err := q.Exec(ctx, AppDatabase, sb.String()); err != nil {
		t.Fatal(err)
	}
}

// untilWhole sets restoring from when the restore's log says it made the database afresh
// until it says it is whole.
type untilWhole struct{ restoring *atomic.Bool }

func (u untilWhole) Write(p []byte) (int, error) {
	switch {
	case strings.Contains(string(p), "into a fresh"):
		u.restoring.Store(true)
	case strings.Contains(string(p), "is put back whole"):
		u.restoring.Store(false)
	}
	return len(p), nil
}

// The outage of 2026-10-28, put back again on a real cluster: while the home's database
// is restored, a joined machine keeps logging in and asking for its copy (its GRANTs on
// every table met pg_restore's: "tuple concurrently updated", and the restore stopped with
// no indexes, keys, triggers or grants), and (the review of #433) a process that runs on
// across the restart as the database's owner keeps writing, which taking CONNECT from
// PUBLIC never stops. The restore goes into a database of its own nobody reaches (the
// machine cannot log in to it), completes in parallel, is checked against the dump (every
// object, every grant, every table's rows: the dump's counts), and is renamed into place
// with the database's own grants (the workers' CONNECT, which a dump does not hold) so the
// machine logs in again.
func TestRealRestoreKeepsOtherLoginsOutAndIsWhole(t *testing.T) {
	b, q := realServer(t)
	ctx := context.Background()
	outageDatabase(t, q, 60)
	dump, err := b.DumpBeforeUpdate(ctx, "v0.3.41", "v0.3.42", 3)
	if err != nil {
		t.Fatal(err)
	}
	counts := DumpCounts(dump)
	if counts["public.segment_embeddings"] != 5000 || counts["public.t010"] != 30 || counts["public.videos"] != 300 {
		t.Fatalf("the dump's counts = %v", counts)
	}
	// what the failed update did
	if err := q.Exec(ctx, AppDatabase, `SET ROLE `+AppRole+`; INSERT INTO goose_db_version (version_id, is_applied)
		VALUES (168, true); DELETE FROM t001; CREATE TABLE added (id int);`); err != nil {
		t.Fatal(err)
	}

	psql := func(ctx context.Context, user, pw, db, sql string) error {
		cmd := exec.CommandContext(ctx, b.Bin("psql"), "-X", "-q", "-At", "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1",
			"-p", strconv.Itoa(b.Port), "-U", user, "-d", db, "-c", sql)
		cmd.Env = append(os.Environ(), "PGPASSWORD="+pw, "PGCONNECT_TIMEOUT=2")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, out)
		}
		return nil
	}
	// the joined machine: logs in and asks for its copy, again and again
	node := func(ctx context.Context, db string) error {
		return psql(ctx, "node_login", "node-pw", db, "SELECT copy_grant()")
	}
	if err := node(ctx, AppDatabase); err != nil {
		t.Fatalf("the machine cannot log in before the restore: %v", err)
	}
	var restoring atomic.Bool
	var tries, scratch, writes atomic.Int32
	stop := make(chan struct{})
	var wg sync.WaitGroup
	loop := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				fn()
			}
		}()
	}
	for w := 0; w < 2; w++ {
		loop(func() {
			_ = node(ctx, AppDatabase) // the database being replaced: as before the rollback
			if restoring.Load() {
				tries.Add(1)
				if node(ctx, restoringDatabase) == nil { // the one being put back: never
					scratch.Add(1)
				}
			}
		})
	}
	// the owner's process that runs on (a TV slot handed over): it writes all along
	loop(func() {
		if psql(ctx, SuperUser, q.pw, AppDatabase, `SET ROLE `+AppRole+`;
			INSERT INTO t000 (note, n) VALUES ('written meanwhile', 1)`) == nil {
			writes.Add(1)
		}
	})
	start := time.Now()
	err = b.RestoreRunning(ctx, dump, untilWhole{&restoring})
	restoring.Store(false)
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatalf("the restore with a machine and the owner's process at it meanwhile: %v", err)
	}
	t.Logf("put back in %s; the machine tried %d times meanwhile, the owner's process wrote %d times",
		time.Since(start), tries.Load(), writes.Load())
	if tries.Load() == 0 || writes.Load() == 0 {
		t.Fatal("the machine or the owner's process never tried while the database was put back")
	}
	if n := scratch.Load(); n > 0 {
		t.Fatalf("the machine logged in to the database being put back %d times", n)
	}

	// whole: what the dump held, the update's change gone
	for sql, want := range map[string]string{
		`SELECT count(*) FROM t001`:                                                                "21",
		`SELECT count(*) FROM segment_embeddings`:                                                  "5000",
		`SELECT max(version_id) FROM goose_db_version`:                                             "167",
		`SELECT to_regclass('added') IS NULL`:                                                      "t",
		`SELECT count(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname LIKE 't%_note'`: "60",
		`SELECT count(*) FROM pg_trigger WHERE tgname LIKE 't%_touch'`:                             "60",
		`SELECT count(*) FROM pg_constraint WHERE contype = 'f'`:                                   "62",
		`SELECT has_table_privilege('maktaba_worker', 't059', 'INSERT')`:                           "t",
		`SELECT has_column_privilege('maktaba_replicas', 't059', 'note', 'SELECT')`:                "t",
		`SELECT count(*) FROM pg_default_acl`:                                                      "2",
		`SELECT has_function_privilege('maktaba_worker', 'copy_grant()', 'EXECUTE')`:               "t",
	} {
		if got := one(t, q, sql); got != want {
			t.Errorf("%s = %s, want %s", sql, got, want)
		}
	}
	// the database's own grants back: the workers' CONNECT (the machine's way in), PUBLIC's
	acl := one(t, q, `SELECT datacl::text FROM pg_database WHERE datname = '`+AppDatabase+`'`)
	if !strings.Contains(acl, "maktaba_worker=c/") || !strings.Contains(acl, "=Tc/") {
		t.Fatalf("the database's grants after the restore = %s", acl)
	}
	if err := node(ctx, AppDatabase); err != nil {
		t.Fatalf("the machine cannot log in after the restore: %v", err)
	}
	if left := one(t, q, `SELECT count(*) FROM pg_database WHERE datname IN ('`+restoringDatabase+`', '`+
		replacedDatabase+`')`); left != "0" {
		t.Fatalf("%s databases of the restore left", left)
	}
}

// A restore whose dump cannot be put back whole leaves the app database as it was: the
// scratch database goes, the one before stays with its rows and its grants.
func TestRealFailedRestoreLeavesTheDatabase(t *testing.T) {
	b, q := realServer(t)
	ctx := context.Background()
	outageDatabase(t, q, 2)
	dump, err := b.DumpBeforeUpdate(ctx, "v0.3.41", "v0.3.42", 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Exec(ctx, AppDatabase, `SET ROLE `+AppRole+`; CREATE TABLE after_update (id int);`); err != nil {
		t.Fatal(err)
	}
	// a dump whose counts say a table had rows it does not hold
	raw, err := os.ReadFile(filepath.Join(dump, CountsFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dump, CountsFile),
		[]byte(strings.Replace(string(raw), `"public.t001":21`, `"public.t001":99`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	err = b.RestoreRunning(ctx, dump, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "public.t001: 21 of 99 rows") {
		t.Fatalf("an incomplete restore = %v", err)
	}
	if one(t, q, `SELECT to_regclass('after_update') IS NOT NULL`) != "t" {
		t.Fatal("the database before the restore is gone")
	}
	if left := one(t, q, `SELECT count(*) FROM pg_database WHERE datname IN ('`+restoringDatabase+`', '`+
		replacedDatabase+`')`); left != "0" {
		t.Fatalf("%s databases of the restore left", left)
	}
	if acl := one(t, q, `SELECT datacl::text FROM pg_database WHERE datname = '`+AppDatabase+`'`); !strings.Contains(acl, "maktaba_worker=c/") {
		t.Fatalf("the database's grants after a failed restore = %s", acl)
	}
}

// A restore that comes out incomplete says what is missing, and is not taken for whole:
// a table's rows short of the dump's counts, an index the dump had.
func TestRealRestoreSaysWhatIsMissing(t *testing.T) {
	b, q := realServer(t)
	ctx := context.Background()
	outageDatabase(t, q, 3)
	dump, err := b.DumpBeforeUpdate(ctx, "v0.3.41", "v0.3.42", 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.RestoreRunning(ctx, dump, io.Discard); err != nil {
		t.Fatal(err)
	}
	toc, err := b.tableOfContents(ctx, dump, q.pw)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRestore(ctx, dump, toc, q.queryFunc(AppDatabase)); err != nil {
		t.Fatalf("a whole restore: %v", err)
	}
	if err := q.Exec(ctx, AppDatabase, `DELETE FROM t002; DROP INDEX t001_note; DELETE FROM segment_embeddings;
		REVOKE ALL ON t000 FROM maktaba_worker, maktaba_replicas; DROP TRIGGER t000_touch ON t000;`); err != nil {
		t.Fatal(err)
	}
	err = VerifyRestore(ctx, dump, toc, q.queryFunc(AppDatabase))
	var m *Missing
	if err == nil || !errors.As(err, &m) {
		t.Fatalf("an incomplete restore = %v", err)
	}
	for _, want := range []string{"t001_note", "t000 t000_touch", "TABLE t000", "public.t002: 0 of 22 rows",
		"public.segment_embeddings: 0 of 5000 rows"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not name %q", err, want)
		}
	}
}
