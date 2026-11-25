package pgbundle

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/testport"
)

// The pause of the machines' copies on a real server: only the machines' own replication
// roles (maktaba_rep_*) are kept out, never the app role (a member of the group too: the
// auto-update e2e's migrations could not log in when it was paused); their sessions are
// ended, and they may not log in until resumed whatever their roles are given meanwhile.
//
// Opt-in: MAKTABA_TEST_PG_BIN, a Postgres 16 install.
func TestCopiesPauseOnARealServer(t *testing.T) {
	home := os.Getenv("MAKTABA_TEST_PG_BIN")
	if home == "" {
		t.Skip("MAKTABA_TEST_PG_BIN unset (a Postgres 16 install)")
	}
	if testing.Short() {
		t.Skip("runs a Postgres server")
	}
	port := testport.Free(t)
	root, err := os.MkdirTemp("", "mkpause") // short: the unix socket path is limited
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
	srv, err := b.serverCommand(b.serverArgs(false))
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.StopCommand().Run(); _ = srv.Wait() })
	deadline := time.Now().Add(60 * time.Second)
	for Ready(ctx, b.Addr(), AppRole, AppDatabase) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the server is not ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	q, err := b.NewQuerier()
	if err != nil {
		t.Fatal(err)
	}
	const rep, other, pw = "maktaba_rep_0123456789abcdef0123456789abcdef", "maktaba_rep_ffffffffffffffffffffffffffffffff", "pause-test-pw"
	for _, s := range []string{
		`DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '` + ReplicaGroup + `') THEN
		    CREATE ROLE ` + ReplicaGroup + ` NOLOGIN; END IF; END $$`,
		`GRANT ` + ReplicaGroup + ` TO ` + AppRole,
		`CREATE ROLE ` + rep + ` LOGIN REPLICATION PASSWORD '` + pw + `' IN ROLE ` + ReplicaGroup,
		`GRANT CONNECT ON DATABASE ` + AppDatabase + ` TO ` + rep,
	} {
		if err := q.Exec(ctx, AppDatabase, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	// the machine's session, as a copy's
	openAs := func(role string) (*sql.DB, error) {
		db, err := sql.Open("postgres", fmt.Sprintf("host=127.0.0.1 port=%d user=%s password=%s dbname=%s sslmode=disable connect_timeout=5",
			port, role, pw, AppDatabase))
		if err != nil {
			return nil, err
		}
		return db, db.PingContext(ctx)
	}
	held, err := openAs(rep)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	held.SetMaxOpenConns(1)
	n, err := b.PauseCopies(ctx)
	if err != nil || n != 1 {
		t.Fatalf("pause: %d %v (want its one session ended)", n, err)
	}
	refused := func(role string) {
		t.Helper()
		if db, err := openAs(role); err == nil || !strings.Contains(err.Error(), "pg_hba.conf rejects") {
			_ = db.Close()
			t.Fatalf("%s while paused: %v", role, err)
		}
	}
	refused(rep)
	// what a machine's grant does (cluster_node_replica_grant): LOGIN again, or a role made
	for _, s := range []string{`ALTER ROLE ` + rep + ` WITH LOGIN REPLICATION PASSWORD '` + pw + `'`,
		`CREATE ROLE ` + other + ` LOGIN REPLICATION PASSWORD '` + pw + `' IN ROLE ` + ReplicaGroup} {
		if err := q.Exec(ctx, AppDatabase, s); err != nil {
			t.Fatal(err)
		}
	}
	refused(rep)
	refused(other)
	if err := Ready(ctx, b.Addr(), AppRole, AppDatabase); err != nil {
		t.Fatalf("the app role after the pause: %v", err)
	}
	// a start of this release keeps them out (killed while paused)
	if err := b.writeHBA(); err != nil || !strings.Contains(b.hbaConf(), "reject") {
		t.Fatalf("a start while paused: %v\n%s", err, b.hbaConf())
	}
	// paused again, with a role dropped meanwhile
	if err := q.Exec(ctx, AppDatabase, `DROP ROLE `+other); err != nil {
		t.Fatal(err)
	}
	if n, err := b.PauseCopies(ctx); err != nil || n != 0 {
		t.Fatalf("pause again: %d %v", n, err)
	}
	if err := b.ResumeCopies(ctx); err != nil || b.CopiesPaused() {
		t.Fatalf("resume: %v (paused %v)", err, b.CopiesPaused())
	}
	if err := until(ctx, 10*time.Second, func() (bool, error) {
		back, err := openAs(rep)
		_ = back.Close()
		return err == nil, nil
	}); err != nil {
		t.Fatalf("a resumed copy: %v", err)
	}
	// a server that cannot be told keeps the marker: it is tried again
	if _, err := b.PauseCopies(ctx); err != nil {
		t.Fatal(err)
	}
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if err := b.ResumeCopies(gone); err == nil || !b.CopiesPaused() {
		t.Fatalf("a resume the server did not take: %v (paused %v)", err, b.CopiesPaused())
	}
	// WatchCopies lets them in once the schema is settled, and not before
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	var settled atomic.Bool
	go b.WatchCopies(wctx, 50*time.Millisecond, settled.Load, io.Discard)
	time.Sleep(300 * time.Millisecond)
	if !b.CopiesPaused() {
		t.Fatal("the copies came back on a schema that is not settled")
	}
	settled.Store(true)
	if err := until(ctx, 10*time.Second, func() (bool, error) { return !b.CopiesPaused(), nil }); err != nil {
		t.Fatal("the copies are kept out for good")
	}
	// a pause that does not take lets them in again (the caller is told: nothing half done)
	if _, err := b.PauseCopies(gone); err == nil {
		t.Fatal("a pause of no server")
	}
	// table syncs: an active slot counts, one left behind does not
	const sync = "pg_16999_sync_16384_7300000000000000003"
	if n, err := b.TableSyncs(ctx); err != nil || n != 0 {
		t.Fatalf("table syncs %d %v", n, err)
	}
	if err := q.Exec(ctx, AppDatabase, `SELECT pg_create_logical_replication_slot('`+sync+`', 'pgoutput')`); err != nil {
		t.Fatal(err)
	}
	if n, err := b.TableSyncs(ctx); err != nil || n != 0 {
		t.Fatalf("a slot left behind: %d %v", n, err)
	}
	_ = q.Exec(ctx, AppDatabase, `SELECT pg_drop_replication_slot('`+sync+`')`)
	// a slot being made (it waits for the transactions under way) is active
	super, err := b.SuperURL()
	if err != nil {
		t.Fatal(err)
	}
	sdb, err := sql.Open("postgres", super)
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	tx, err := sdb.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`SELECT pg_current_xact_id()`); err != nil {
		t.Fatal(err)
	}
	made := make(chan error, 1)
	go func() {
		made <- q.Exec(ctx, AppDatabase, `SELECT pg_create_logical_replication_slot('`+sync+`', 'pgoutput')`)
	}()
	if err := until(ctx, 20*time.Second, func() (bool, error) {
		n, err := b.TableSyncs(ctx)
		return n == 1, err
	}); err != nil {
		t.Fatalf("an active table sync is not counted: %v", err)
	}
	_ = tx.Rollback()
	if err := <-made; err != nil {
		t.Fatal(err)
	}
	_ = q.Exec(ctx, AppDatabase, `SELECT pg_drop_replication_slot('`+sync+`')`)
}
