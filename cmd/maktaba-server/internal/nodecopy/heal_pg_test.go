package nodecopy

// A copy that cannot follow mends itself (specs/epics/37-node-replica, "A copy that
// cannot follow"): the home re-makes this machine's access (a new password for its role)
// and the copy's calls to the home log in with the new one while its subscription keeps
// following; the home refuses the copy's own login, and its subscription is paused rather
// than let knock every few seconds, then on again once the home lets it in; the home's
// slot for the copy is lost (it was away past the home's cap), and the copy is made anew
// and follows again. Opt-in as TestCopyFollowsTheHome: MAKTABA_TEST_PG_BIN.

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// rig is a home's Postgres (as the bundled one, migrated), an approved full machine and
// its copy, kept by a Manager the test can stop and start again.
type rig struct {
	t      *testing.T
	ctx    context.Context
	pgHome string
	home   pgbundle.Bundle
	app    *sql.DB // the home as its app role
	super  *sql.DB // the home as its superuser
	role   string  // the machine's role on the home
	dsn    atomic.Value
	c      Copy
	cp     *server
	m      *Manager
	stop   func()
	mlog   bytes.Buffer
	mlogMu sync.Mutex
	copyDB *sql.DB
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pgHome := os.Getenv("MAKTABA_TEST_PG_BIN")
	if pgHome == "" {
		t.Skip("MAKTABA_TEST_PG_BIN unset (a Postgres 16 install)")
	}
	if testing.Short() {
		t.Skip("runs two Postgres servers and builds maktaba-api")
	}
	root := repoRoot(t)
	tmp := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := &rig{t: t, ctx: ctx, pgHome: pgHome}

	api := filepath.Join(tmp, "maktaba-api"+map[bool]string{true: ".exe", false: ""}[runtime.GOOS == "windows"])
	build := exec.Command("go", "build", "-o", api, ".")
	build.Dir = filepath.Join(root, "api")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build maktaba-api: %v\n%s", err, out)
	}
	migrations := filepath.Join(root, "shared", "db", "migrations")
	migrate := func(ctx context.Context, dsn string) error {
		cmd := exec.CommandContext(ctx, api, "migrate", "--dir", migrations, "--dsn", dsn, "up")
		cmd.Env = append(os.Environ(), "MAKTABA_NODE_MODE=")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%v: %s", err, out)
		}
		return nil
	}

	r.home = pgbundle.Bundle{Home: pgHome, Data: filepath.Join(tmp, "home"), Keys: filepath.Join(tmp, "home-keys"),
		Backups: filepath.Join(tmp, "backups"), Port: freePort(t), TLS: true}
	if err := r.home.Prepare(ctx, io.Discard); err != nil {
		t.Fatalf("prepare the home: %v", err)
	}
	hs := &server{}
	hs.start(t, r.home.Bin("postgres"), r.home.ServerArgs()...)
	t.Cleanup(func() { hs.stop(r.home.StopCommand()) })
	waitFor(t, "the home's Postgres", 60*time.Second, func() bool {
		rctx, c := context.WithTimeout(ctx, 2*time.Second)
		defer c()
		return pgbundle.Ready(rctx, r.home.Addr(), pgbundle.AppRole, pgbundle.AppDatabase) == nil
	})
	appURL, err := r.home.URL()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, appURL); err != nil {
		t.Fatalf("migrate the home: %v", err)
	}
	if r.app, err = sql.Open("postgres", appURL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.app.Close() })
	superURL, err := r.home.SuperURL()
	if err != nil {
		t.Fatal(err)
	}
	if r.super, err = sql.Open("postgres", superURL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.super.Close() })
	ca, err := os.ReadFile(r.home.CACert())
	if err != nil {
		t.Fatal(err)
	}
	r.exec(`UPDATE cluster_replica_beat SET ca_pem = $1`, string(ca))
	// the home's API beats while a machine follows (api cluster.Copies)
	go func() {
		for ctx.Err() == nil {
			_, _ = r.app.ExecContext(ctx, `UPDATE cluster_replica_beat SET at = now()`)
			time.Sleep(time.Second)
		}
	}()

	nodeID := uuid.NewString()
	r.role = "maktaba_node_" + strings.ReplaceAll(nodeID, "-", "")
	r.exec(`CREATE ROLE ` + r.role + ` LOGIN PASSWORD 'node-pass-123' IN ROLE maktaba_worker`)
	r.exec(`INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, home_privkey, db_role, approved_at, approved_by,
	       full_node) VALUES ($1, 'studio', 'approved', 'windows', 'amd64', 'k', 'node-secret-key', $2, now(), 'test', true)`,
		nodeID, r.role)
	r.dsn.Store(r.nodeDSN("node-pass-123"))
	r.c = Copy{NodeID: nodeID, HomeDSN: r.nodeDSN("node-pass-123"), Migrate: migrate,
		// node.json as it is now (a re-made access changes it)
		HomeDSNOf: func() string { return r.dsn.Load().(string) },
		DB: pgbundle.Bundle{Data: filepath.Join(tmp, "node-pg"), Keys: filepath.Join(tmp, "node-keys"),
			Backups: filepath.Join(tmp, "node-backups"), Port: freePort(t)}}
	r.cp = &server{}
	t.Cleanup(func() { r.stopManager(); r.cp.stop(r.db().StopCommand()) })
	r.startManager()
	return r
}

func (r *rig) db() pgbundle.Bundle { d := r.c.DB; d.Home = r.pgHome; return d }

func (r *rig) nodeDSN(password string) string {
	return (&url.URL{Scheme: "postgres", User: url.UserPassword(r.role, password), Host: r.home.Addr(),
		Path: "/" + pgbundle.AppDatabase, RawQuery: url.Values{"sslmode": {"verify-ca"},
			"sslrootcert": {filepath.ToSlash(r.home.CACert())}}.Encode()}).String()
}

func (r *rig) exec(q string, args ...any) {
	r.t.Helper()
	if _, err := r.app.ExecContext(r.ctx, q, args...); err != nil {
		r.t.Fatalf("%s: %v", q, err)
	}
}

func (r *rig) superExec(q string, args ...any) {
	r.t.Helper()
	if _, err := r.super.ExecContext(r.ctx, q, args...); err != nil {
		r.t.Fatalf("%s: %v", q, err)
	}
}

// startManager runs a Manager on the copy (as a start of serve does).
func (r *rig) startManager() {
	m := &Manager{Copy: r.c, StateDir: filepath.Join(filepath.Dir(r.c.DB.Data), "node-state"),
		Programs: func(context.Context) (string, error) { return r.pgHome, nil },
		Every:    300 * time.Millisecond, Retry: 300 * time.Millisecond, WipeAfter: 2, WipeOver: time.Nanosecond,
		CopyLoginEvery: 300 * time.Millisecond, AuthBackoffMax: 4 * time.Second,
		Log: lockedWriter{w: &r.mlog, mu: &r.mlogMu}}
	m.Want = func(on bool) error {
		if on {
			r.cp.start(r.t, r.db().Bin("postgres"), r.db().ServerArgs()...)
		} else {
			r.cp.stop(r.db().StopCommand())
		}
		return nil
	}
	m.Running = r.cp.running
	mctx, cancel := context.WithCancel(r.ctx)
	done := make(chan struct{})
	go func() { m.Run(mctx); close(done) }()
	r.m, r.stop = m, func() { cancel(); <-done }
}

func (r *rig) stopManager() {
	if r.stop != nil {
		r.stop()
		r.stop = nil
	}
}

func (r *rig) dump() string {
	r.mlogMu.Lock()
	defer r.mlogMu.Unlock()
	return "manager:\n" + r.mlog.String() + "\ncopy:\n" + r.cp.log.String()
}

func (r *rig) following(what string) {
	r.t.Helper()
	end := time.Now().Add(4 * time.Minute)
	for r.m.Status().State != StateFollowing {
		if time.Now().After(end) {
			r.t.Fatalf("%s: the copy is %q (%s), want following\n%s", what, r.m.Status().State, r.m.Status().Detail, r.dump())
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// follows: a change on the home shows on the copy.
func (r *rig) follows(what, value string) {
	r.t.Helper()
	if r.copyDB == nil {
		dsn, err := r.c.DSN()
		if err != nil {
			r.t.Fatal(err)
		}
		if r.copyDB, err = sql.Open("postgres", dsn); err != nil {
			r.t.Fatal(err)
		}
		r.t.Cleanup(func() { _ = r.copyDB.Close() })
	}
	r.exec(`INSERT INTO app_settings (key, value, updated_at) VALUES ('test.heal', to_jsonb($1::text), now())
	        ON CONFLICT (key) DO UPDATE SET value = excluded.value`, value)
	end := time.Now().Add(time.Minute)
	for {
		var v string
		err := r.copyDB.QueryRowContext(r.ctx, `SELECT value::text FROM app_settings WHERE key = 'test.heal'`).Scan(&v)
		if err == nil && strings.Contains(v, value) {
			return
		}
		if time.Now().After(end) {
			r.t.Fatalf("%s: %q not on the copy (%q, %v)\n%s", what, value, v, err, r.dump())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (r *rig) subEnabled() bool {
	db, err := r.c.super()
	if err != nil {
		r.t.Fatal(err)
	}
	defer db.Close()
	s, err := r.c.subscription(r.ctx, db)
	if err != nil {
		r.t.Fatal(err)
	}
	return s.exists && s.enabled
}

func (r *rig) slotWAL() string {
	var st sql.NullString
	_ = r.super.QueryRowContext(r.ctx, `SELECT wal_status FROM pg_replication_slots WHERE slot_name = $1`, r.c.Slot()).Scan(&st)
	return st.String
}

// The home re-makes this machine's access: the copy's calls log in with the new password
// (node.json's now) and its subscription, on a role of its own, follows on. The home then
// refuses the copy's own login: its subscription is paused, not left to knock every few
// seconds, and is on again, following, once the home lets it in.
func TestCopyFollowsARemadeAccess(t *testing.T) {
	r := newRig(t)
	r.following("the first copy")
	r.follows("the first copy", "one")

	// the home re-makes the machine's access: a new password, its sessions ended; this
	// machine follows it (node.json)
	r.exec(`ALTER ROLE ` + r.role + ` PASSWORD 'node-pass-456'`)
	r.superExec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1`, r.role)
	r.dsn.Store(r.nodeDSN("node-pass-456"))
	r.follows("after the re-made access", "two")
	// it asked the home (every few turns) and told it, with the new password: no refused login
	asked := time.Now()
	waitFor(t, "a report after the re-made access", 3*time.Minute, func() bool {
		var at sql.NullTime
		_ = r.app.QueryRowContext(r.ctx, `SELECT replica_reported_at FROM cluster_nodes WHERE db_role = $1`, r.role).Scan(&at)
		return at.Valid && at.Time.After(asked.Add(-time.Second)) && r.m.Status().State == StateFollowing
	})
	time.Sleep(3 * time.Second) // a few of its turns
	r.m.mu.Lock()
	refused := r.m.homeAuth
	r.m.mu.Unlock()
	if refused != 0 {
		t.Fatalf("the home refused this machine's login %d times after its access was re-made\n%s", refused, r.dump())
	}

	// the home refuses the copy's own login (a pg_hba rule for its replication role)
	hba := filepath.Join(r.home.Data, "pg_hba.conf")
	was, err := os.ReadFile(hba)
	if err != nil {
		t.Fatal(err)
	}
	rep := r.c.Role()
	reject := fmt.Sprintf("hostssl all %s 127.0.0.1/32 reject\nhost all %s 127.0.0.1/32 reject\n", rep, rep)
	if err := os.WriteFile(hba, append([]byte(reject), was...), 0o600); err != nil {
		t.Fatal(err)
	}
	r.superExec(`SELECT pg_reload_conf()`)
	r.superExec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1`, rep)
	waitFor(t, "the copy paused after its refused logins", time.Minute, func() bool {
		s := r.m.Status()
		return s.State == StateError && strings.Contains(s.Detail, "refused this copy's login") && !r.subEnabled()
	})
	// while paused nothing of it knocks at the home: its subscription is off
	time.Sleep(500 * time.Millisecond)
	if r.copyPausedNow() && r.subEnabled() {
		t.Fatalf("the copy's subscription is on while it is paused\n%s", r.dump())
	}
	// the home lets it in again: on again after its pause, and following
	if err := os.WriteFile(hba, was, 0o600); err != nil {
		t.Fatal(err)
	}
	r.superExec(`SELECT pg_reload_conf()`)
	r.following("after the home let the copy in again")
	r.follows("after the home let the copy in again", "three")
	r.mlogMu.Lock()
	log := r.mlog.String()
	r.mlogMu.Unlock()
	if !strings.Contains(log, "its subscription is paused for") {
		t.Fatalf("no pause in the manager's log\n%s", r.dump())
	}
}

func (r *rig) copyPausedNow() bool { return r.m.copyPaused() }

// The home's slot for the copy is lost (the machine was off past the home's cap): the
// copy, started again, sees it, is made anew (said as a rebuild) and follows again.
func TestCopyRebuildsALostSlot(t *testing.T) {
	r := newRig(t)
	r.following("the first copy")
	r.follows("the first copy", "one")
	first, _ := r.c.Mark()

	// the machine goes away, its copy paused
	r.stopManager()
	if err := r.c.Pause(r.ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the slot let go", time.Minute, func() bool {
		var active bool
		err := r.super.QueryRowContext(r.ctx, `SELECT active FROM pg_replication_slots WHERE slot_name = $1`, r.c.Slot()).Scan(&active)
		return err == nil && !active
	})
	// past the home's cap: Postgres gives the slot's changes up
	r.superExec(`ALTER SYSTEM SET max_slot_wal_keep_size = '1MB'`)
	r.superExec(`SELECT pg_reload_conf()`)
	waitFor(t, "the slot lost", 2*time.Minute, func() bool {
		r.exec(`INSERT INTO app_settings (key, value, updated_at) VALUES ('test.wal', to_jsonb(repeat('x', 200000)), now())
		        ON CONFLICT (key) DO UPDATE SET value = to_jsonb(repeat(md5(random()::text), 6000)), updated_at = now()`)
		r.superExec(`SELECT pg_switch_wal()`)
		r.superExec(`CHECKPOINT`)
		return r.slotWAL() == SlotLost
	})
	r.superExec(`ALTER SYSTEM RESET max_slot_wal_keep_size`)
	r.superExec(`SELECT pg_reload_conf()`)
	r.exec(`UPDATE app_settings SET value = '"while away"' WHERE key = 'test.wal'`)

	// the machine is back: its copy is made anew, said as a rebuild
	r.startManager()
	waitFor(t, "the copy made anew", 3*time.Minute, func() bool {
		mk, ok := r.c.Mark()
		return ok && mk.Subscribed.After(first.Subscribed)
	})
	r.following("after its slot was lost")
	r.follows("after its slot was lost", "two")
	if st := r.slotWAL(); st == SlotLost || st == "" {
		t.Fatalf("the slot after the rebuild: %q", st)
	}
	// said as a rebuild, with why (the state the home's Machines page shows)
	r.mlogMu.Lock()
	log := r.mlog.String()
	r.mlogMu.Unlock()
	if !strings.Contains(log, StateRebuilding+": the home's slot for this copy lost") {
		t.Fatalf("the copy was never said to be rebuilding for its lost slot\n%s", r.dump())
	}
}

// rows is q's rows, one line each (its columns text), for a failure's message.
func rows(ctx context.Context, db *sql.DB, q string) string {
	rs, err := db.QueryContext(ctx, q)
	if err != nil {
		return err.Error()
	}
	defer rs.Close()
	cols, _ := rs.Columns()
	var b strings.Builder
	for rs.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return err.Error()
		}
		for i, v := range vals {
			if i > 0 {
				b.WriteString(" | ")
			}
			b.WriteString(v.String)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// A table's first copy waits on a home that sends nothing (2026-10-28: the home's
// server hung mid-copy while the connection lived on). Its worker never sees the
// subscription off, so stopping the copy before a fresh one ends it rather than failing
// every turn for as long as the home hangs.
func TestCopyQuiesceEndsAWorkerStuckOnTheHome(t *testing.T) {
	r := newRig(t)
	r.following("the first copy")
	r.follows("the first copy", "one")
	r.stopManager()
	db, err := r.c.super()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := r.c.Pause(r.ctx); err != nil {
		t.Fatal(err)
	}

	// the home's copy of app_settings waits on a lock held there: its worker here waits
	// for rows that do not come
	hold, err := r.super.BeginTx(r.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback() }()
	if _, err := hold.ExecContext(r.ctx, `LOCK TABLE public.app_settings IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(r.ctx, `TRUNCATE public.app_settings`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(r.ctx, `UPDATE pg_subscription_rel SET srsubstate = 'i', srsublsn = NULL
	    WHERE srrelid = 'public.app_settings'::regclass`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(r.ctx, `ALTER SUBSCRIPTION `+Subscription+` ENABLE`); err != nil {
		t.Fatal(err)
	}
	end := time.Now().Add(2 * time.Minute)
	for {
		var n int
		// (its sync slot waits for the transaction holding the lock, or its COPY for the lock)
		_ = r.super.QueryRowContext(r.ctx, `SELECT count(*) FROM pg_stat_activity
		    WHERE backend_type = 'walsender' AND wait_event_type = 'Lock'`).Scan(&n)
		if n > 0 {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("the table's copy never waited on the home\nhome:\n%s\ncopy:\n%s\n%s", rows(r.ctx, r.super,
				`SELECT backend_type, coalesce(wait_event_type, ''), coalesce(wait_event, ''), left(query, 120)
				 FROM pg_stat_activity WHERE pid <> pg_backend_pid()`),
				rows(r.ctx, db, `SELECT coalesce(pid::text, '-'), coalesce(relid::regclass::text, 'apply'),
				 (SELECT string_agg(srrelid::regclass::text || ':' || srsubstate::text, ' ') FROM pg_subscription_rel WHERE srsubstate <> 'r')
				 FROM pg_stat_subscription`), r.dump())
		}
		time.Sleep(300 * time.Millisecond)
	}
	var worker int
	if err := db.QueryRowContext(r.ctx, `SELECT count(*) FROM pg_stat_subscription
	    WHERE subname = $1 AND relid = 'public.app_settings'::regclass AND pid IS NOT NULL`, Subscription).Scan(&worker); err != nil || worker != 1 {
		t.Fatalf("the table's worker: %d %v", worker, err)
	}

	if err := r.c.Quiesce(r.ctx, 2*time.Second); err != nil {
		t.Fatalf("Quiesce: %v\n%s", err, r.dump())
	}
	var live int
	if err := db.QueryRowContext(r.ctx, `SELECT count(*) FROM pg_stat_subscription
	    WHERE subname = $1 AND pid IS NOT NULL`, Subscription).Scan(&live); err != nil || live != 0 {
		t.Fatalf("workers after Quiesce: %d %v", live, err)
	}
	if r.subEnabled() {
		t.Fatal("the subscription is on after Quiesce")
	}
}

// The copy's switch-off waits for the subscription's lock at most its wait: whatever holds
// it (here a session of the test's; a table's copy that ended while its home hung holds it
// until the home answers) never keeps a fresh copy waiting for ever.
func TestCopyQuiesceWaitsBoundedForTheSubscriptionsLock(t *testing.T) {
	r := newRig(t)
	r.following("the first copy")
	r.stopManager()
	db, err := r.c.super()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hold, err := db.BeginTx(r.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback() }()
	if _, err := hold.ExecContext(r.ctx, `ALTER SUBSCRIPTION `+Subscription+` SET (synchronous_commit = 'off')`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- r.c.Quiesce(r.ctx, time.Second) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "switching the copy off") {
			t.Fatalf("Quiesce with the subscription's lock held: %v, want its switch-off refused", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("Quiesce waited for the subscription's lock past its wait\n%s", r.dump())
	}
	_ = hold.Rollback()
	if err := r.c.Quiesce(r.ctx, time.Second); err != nil {
		t.Fatalf("Quiesce once the lock is free: %v", err)
	}
	if r.subEnabled() {
		t.Fatal("the subscription is on after Quiesce")
	}
}

// A table's first copy is stranded: copied, but the table-sync slot it resumes from is
// gone from the home (Postgres's table-sync worker drops it on the home before it commits
// the table done, and was stopped in between). Its worker would ask the home for that
// slot every few seconds, for ever; the copy is made anew instead, and follows.
func TestCopyRebuildsAStrandedTable(t *testing.T) {
	r := newRig(t)
	r.following("the first copy")
	r.follows("the first copy", "one")
	first, _ := r.c.Mark()

	// the table-sync worker stopped between dropping its slot on the home and committing
	// app_settings done: the table is left copied ('f'), its slot gone
	r.stopManager()
	db, err := r.c.super()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := r.c.Pause(r.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(r.ctx, `UPDATE pg_subscription_rel SET srsubstate = 'f', srsublsn = NULL
	    WHERE srrelid = 'public.app_settings'::regclass`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(r.ctx, `ALTER SUBSCRIPTION `+Subscription+` ENABLE`); err != nil {
		t.Fatal(err)
	}
	if got, err := r.c.Stranded(r.ctx, 100*time.Millisecond); err != nil || len(got) != 1 || got[0] != "app_settings" {
		t.Fatalf("the stranded tables: %v %v", got, err)
	}
	// its worker asks the home for the slot that is not there
	waitFor(t, "the table-sync worker failing", time.Minute, func() bool {
		var n int64
		_ = db.QueryRowContext(r.ctx, `SELECT sync_error_count FROM pg_stat_subscription_stats
		    WHERE subname = $1`, Subscription).Scan(&n)
		return n > 0
	})

	r.startManager()
	waitFor(t, "the copy made anew", 3*time.Minute, func() bool {
		mk, ok := r.c.Mark()
		return ok && mk.Subscribed.After(first.Subscribed)
	})
	r.following("after the stranded table")
	r.follows("after the stranded table", "two")
	r.mlogMu.Lock()
	log := r.mlog.String()
	r.mlogMu.Unlock()
	if !strings.Contains(log, "the first copy of app_settings cannot finish") {
		t.Fatalf("the copy was not made anew for its stranded table\n%s", r.dump())
	}
	if got, err := r.c.Stranded(r.ctx, 0); err != nil || len(got) != 0 {
		t.Fatalf("stranded after the rebuild: %v %v", got, err)
	}
}
