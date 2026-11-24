package pgbundle

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/testport"
)

// The pause of the machines' copies on a real server: only the machines' own replication
// roles (maktaba_rep_*) are listed, never the app role (a member of the group too: the
// auto-update e2e's migrations could not log in when it was paused), their sessions are
// ended, they may not log in until resumed, and a table sync's slot is counted.
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
	const rep, pw = "maktaba_rep_0123456789abcdef0123456789abcdef", "pause-test-pw"
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
	roles, err := b.ReplicaLogins(ctx)
	if err != nil || !slices.Equal(roles, []string{rep}) {
		t.Fatalf("replica logins %v %v: want only the machine's role", roles, err)
	}
	// the machine's session, as a copy's
	open := func() (*sql.DB, error) {
		db, err := sql.Open("postgres", fmt.Sprintf("host=127.0.0.1 port=%d user=%s password=%s dbname=%s sslmode=disable connect_timeout=5",
			port, rep, pw, AppDatabase))
		if err != nil {
			return nil, err
		}
		return db, db.PingContext(ctx)
	}
	held, err := open()
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	held.SetMaxOpenConns(1)
	n, err := b.PauseCopies(ctx, roles)
	if err != nil || n != 1 {
		t.Fatalf("pause: %d %v (want its one session ended)", n, err)
	}
	if again, err := open(); err == nil {
		again.Close()
		t.Fatal("a paused copy logged in")
	}
	if err := Ready(ctx, b.Addr(), AppRole, AppDatabase); err != nil {
		t.Fatalf("the app role after the pause: %v", err)
	}
	if err := b.ResumeCopies(ctx, roles); err != nil {
		t.Fatal(err)
	}
	back, err := open()
	if err != nil {
		t.Fatalf("a resumed copy: %v", err)
	}
	back.Close()
	// a table sync's slot (logical decoding is on in the bundled server)
	if n, err := b.TableSyncs(ctx); err != nil || n != 0 {
		t.Fatalf("table syncs %d %v", n, err)
	}
	if err := q.Exec(ctx, AppDatabase, `SELECT pg_create_logical_replication_slot('pg_16999_sync_16384_7300000000000000003', 'pgoutput')`); err != nil {
		t.Fatal(err)
	}
	if n, err := b.TableSyncs(ctx); err != nil || n != 1 {
		t.Fatalf("table syncs %d %v, want 1", n, err)
	}
	_ = q.Exec(ctx, AppDatabase, `SELECT pg_drop_replication_slot('pg_16999_sync_16384_7300000000000000003')`)
}
