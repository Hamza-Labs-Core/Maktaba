package pgbundle

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/testport"
)

// pauseServer is a home's bundled Postgres, running (MAKTABA_TEST_PG_BIN).
func pauseServer(t *testing.T) (Bundle, Querier) {
	t.Helper()
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
	return b, q
}

// The pause of the machines' copies on a real server: only the machines' own replication
// roles (maktaba_rep_*) are kept out, never the app role (a member of the group too: the
// auto-update e2e's migrations could not log in when it was paused); their sessions are
// ended, and they may not log in until resumed whatever their roles are given meanwhile.
//
// Opt-in: MAKTABA_TEST_PG_BIN, a Postgres 16 install.
func TestCopiesPauseOnARealServer(t *testing.T) {
	ctx := context.Background()
	b, q := pauseServer(t)
	port := b.Port
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
}

// apiSQL is a query of the API's own (a raw string constant of its source).
func apiSQL(t *testing.T, file, name string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "api", filepath.FromSlash(file)))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("(?s)const " + name + " = `([^`]+)`").FindSubmatch(src)
	if m == nil {
		t.Fatalf("%s has no %s", file, name)
	}
	return string(m[1])
}

// What the home's updates look for before a restart (api/internal/updates, runningCopies) is a
// machine's copy of a table as logical replication makes it: a subscription's table sync in its
// COPY, whose slot is not active meanwhile. Seen by the app role and by a role with no rights on
// others' sessions; a sync of a machine that went away in the middle leaves its slot and is none.
//
// The machine is another database of the same server.
func TestAMachinesCopyIsSeenWhileItCopies(t *testing.T) {
	ctx := context.Background()
	b, q := pauseServer(t)
	running := apiSQL(t, "internal/updates/scheduler.go", "runningCopies")
	const rep, plain, pw, machine = "maktaba_rep_0123456789abcdef0123456789abcdef", "plain_app", "copy-test-pw", "machine"
	do := func(db, s string) {
		t.Helper()
		if err := q.Exec(ctx, db, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	one := func(db, s string) string {
		t.Helper()
		rows, err := q.Query(ctx, db, s)
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s: %v %v", s, rows, err)
		}
		return strings.TrimSpace(rows[0])
	}
	open := func(dsn string) *sql.DB {
		t.Helper()
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	login := func(role, db string) string {
		return fmt.Sprintf("host=127.0.0.1 port=%d user=%s password=%s dbname=%s sslmode=disable connect_timeout=5", b.Port, role, pw, db)
	}
	do("postgres", `CREATE DATABASE `+machine)
	do(AppDatabase, `CREATE ROLE `+rep+` LOGIN REPLICATION PASSWORD '`+pw+`'`)
	do(AppDatabase, `CREATE ROLE `+plain+` LOGIN PASSWORD '`+pw+`'`)
	do(AppDatabase, `GRANT CONNECT ON DATABASE `+AppDatabase+` TO `+rep+`, `+plain)
	appURL, err := b.URL()
	if err != nil {
		t.Fatal(err)
	}
	asApp, asPlain := open(appURL), open(login(plain, AppDatabase))
	seen := func() int {
		t.Helper()
		var n, m int
		if err := asApp.QueryRow(running).Scan(&n); err != nil {
			t.Fatalf("the app role: %v", err)
		}
		if err := asPlain.QueryRow(running).Scan(&m); err != nil {
			t.Fatalf("a role with no rights: %v", err)
		}
		return min(n, m)
	}
	// tables too large for the sockets' buffers, which the machine stops taking in
	for _, tbl := range []string{"one", "two"} {
		do(AppDatabase, `CREATE TABLE public.`+tbl+` (id int PRIMARY KEY, v text)`)
		do(AppDatabase, `INSERT INTO public.`+tbl+` SELECT i, repeat(md5(i::text), 40) FROM generate_series(1, 40000) i`)
		do(AppDatabase, `GRANT SELECT ON public.`+tbl+` TO `+rep)
		do(machine, `CREATE TABLE public.`+tbl+` (id int PRIMARY KEY, v text)`)
	}
	do(machine, `CREATE FUNCTION public.hold() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(7); RETURN NEW; END $$`)
	for _, tbl := range []string{"one", "two"} {
		do(machine, `CREATE TRIGGER hold BEFORE INSERT ON public.`+tbl+` FOR EACH ROW EXECUTE FUNCTION public.hold()`)
		do(machine, `ALTER TABLE public.`+tbl+` ENABLE ALWAYS TRIGGER hold`)
	}
	super, err := b.SuperURL()
	if err != nil {
		t.Fatal(err)
	}
	machineURL, err := url.Parse(super)
	if err != nil {
		t.Fatal(err)
	}
	machineURL.Path = "/" + machine
	gate := open(machineURL.String())
	gate.SetMaxOpenConns(1)
	stop := func(on string) {
		t.Helper()
		if _, err := gate.Exec(`SELECT pg_advisory_` + on + `(7)`); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(what string, ok func() bool) {
		t.Helper()
		if err := until(ctx, 60*time.Second, func() (bool, error) { return ok(), nil }); err != nil {
			t.Fatalf("%s: %v (slots %s)", what, err,
				one("postgres", `SELECT coalesce(string_agg(slot_name || ' ' || active, ', '), '') FROM pg_replication_slots`))
		}
	}
	const syncSlots = `SELECT count(*) || ' ' || count(*) FILTER (WHERE active) FROM pg_replication_slots WHERE slot_name ~ '^pg_[0-9]+_sync_'`
	if n := seen(); n != 0 {
		t.Fatalf("%d copies before there is one", n)
	}
	stop("lock")
	do(AppDatabase, `CREATE PUBLICATION p FOR TABLE public.one`)
	do(AppDatabase, `SELECT pg_create_logical_replication_slot('m', 'pgoutput')`)
	do(machine, `CREATE SUBSCRIPTION m CONNECTION '`+login(rep, AppDatabase)+`' PUBLICATION p WITH (create_slot = false, slot_name = 'm')`)
	wait("a machine's copy is not seen", func() bool { return seen() == 1 })
	// what the slots say of it: nothing
	wait("the table sync's slot", func() bool { return one("postgres", syncSlots) == "1 0" })
	if n := seen(); n != 1 {
		t.Fatalf("%d copies while one runs", n)
	}
	stop("unlock")
	wait("the copy's end", func() bool {
		return seen() == 0 && one(machine, `SELECT count(*) FROM public.one`) == "40000" && one("postgres", syncSlots) == "0 0"
	})
	// following, its walsender streams: no copy
	if n := one("postgres", `SELECT count(*) FROM pg_stat_replication WHERE usename = '`+rep+`'`); n != "1" {
		t.Fatalf("%s walsenders follow, want 1", n)
	}
	// a machine gone in the middle of the next table
	stop("lock")
	do(AppDatabase, `ALTER PUBLICATION p ADD TABLE public.two`)
	do(machine, `ALTER SUBSCRIPTION m REFRESH PUBLICATION`)
	wait("the second table's copy is not seen", func() bool { return seen() == 1 })
	do(machine, `ALTER SUBSCRIPTION m DISABLE`)
	// (the disabled subscription's worker under way in its COPY goes on: end it, as a machine's end does)
	do(machine, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '`+machine+`' AND backend_type LIKE 'logical replication%'`)
	wait("a machine that went away", func() bool { return seen() == 0 })
	if s := one("postgres", syncSlots); s != "1 0" {
		t.Fatalf("the table sync's slots (all, active): %s, want the one left behind", s)
	}
}
