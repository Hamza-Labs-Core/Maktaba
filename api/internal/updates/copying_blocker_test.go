package updates

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Research item 5: no automatic run starts while a machine copies a table of the home's
// database; a database that cannot tell (SQLite) is not held. On Postgres: a COPY of a
// machine's replication role holds it, another role's does not, and neither does a table
// sync's slot a machine that went away left (permanent), nor one being made (active). The
// same query against a real subscription's table sync, on 16 and 18:
// cmd/maktaba-server/internal/pgbundle, TestAMachinesCopyIsSeenWhileItCopies.
func TestCopyingBlocker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			b := CopyingBlocker{DB: be.db}
			blocked := func() string {
				t.Helper()
				why, err := b.Blocked(ctx)
				if err != nil {
					t.Fatal(err)
				}
				return why
			}
			if why := blocked(); why != "" {
				t.Fatalf("no copy: %q", why)
			}
			if be.name == "sqlite" {
				return
			}
			suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
			for _, c := range []struct{ role, want string }{
				{"maktaba_rep_" + suffix, BlockMachineCopying},
				{"maktaba_node_" + suffix, ""}, // a machine's own role
				{"maktabaxrepx" + suffix, ""},  // the underscores are not wildcards
			} {
				end := copyAs(t, be.db, c.role)
				if why := blocked(); why != c.want {
					t.Errorf("a COPY of %s: %q, want %q", c.role, why, c.want)
				}
				end()
				if why := blocked(); why != "" {
					t.Errorf("the COPY of %s ended: %q", c.role, why)
				}
			}
			const slot = "pg_16999_sync_16384_7300000000000000002"
			if _, err := be.db.Exec(`SELECT pg_create_logical_replication_slot($1, 'pgoutput')`, slot); err != nil {
				t.Skipf("no logical slot on this server: %v", err)
			}
			t.Cleanup(func() { _, _ = be.db.Exec(`SELECT pg_drop_replication_slot($1)`, slot) })
			if why := blocked(); why != "" {
				t.Fatalf("a slot left behind: %q", why)
			}
		})
	}
	if why, _ := (CopyingBlocker{}).Blocked(ctx); why != "" {
		t.Fatalf("no database: %q", why)
	}
}

// copyAs is a COPY under way of a session of role (made for it), until the end it returns.
func copyAs(t *testing.T, db *sql.DB, role string) (end func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var name string
	for _, q := range []string{`CREATE ROLE ` + role + ` LOGIN PASSWORD 'copy-test-pw'`,
		`GRANT pg_write_server_files TO ` + role} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP ROLE IF EXISTS ` + role) })
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("MAKTABA_TEST_PG_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.User, u.Path = url.UserPassword(role, "copy-test-pw"), "/"+name
	as, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := as.Ping(); err != nil {
		cancel()
		t.Skipf("%s cannot log in here: %v", role, err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := as.ExecContext(ctx, `COPY (SELECT pg_sleep(120)) TO '/dev/null'`)
		done <- err
	}()
	// the server's own word that it runs, whoever's it is
	for stop := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_progress_copy c JOIN pg_stat_activity a USING (pid)
			WHERE a.usename = $1`, role).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			break
		}
		select {
		case err := <-done:
			cancel()
			t.Fatalf("the COPY of %s: %v", role, err)
		default:
		}
		if time.Now().After(stop) {
			t.Fatalf("no COPY of %s runs", role)
		}
	}
	return func() {
		t.Helper()
		cancel()
		<-done
		_ = as.Close()
		for stop := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			var n int
			if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE usename = $1`, role).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				return
			}
			if time.Now().After(stop) {
				t.Fatalf("the session of %s does not end", role)
			}
		}
	}
}

// The home's scheduler runs with it.
func TestHomeBlockersLookForCopies(t *testing.T) {
	t.Parallel()
	db := &sql.DB{}
	for _, b := range HomeBlockers(db, "", nil) {
		if c, ok := b.(CopyingBlocker); ok && c.DB == db {
			return
		}
	}
	t.Fatal("no CopyingBlocker of the home's database among the home's blockers")
}

// Machines' copies hold automatic runs a day at most: one that never ends (made anew again
// and again, a machine asleep in the middle) is gone past then, audited.
func TestCopiesHoldAutomaticRunsADayAtMost(t *testing.T) {
	t.Parallel()
	enableAutoInstall(t)
	ctx := context.Background()
	f := newFixture(t, sqliteDB(t))
	seed(t, f.store, rel("1.5.0"))
	copying, other := BlockMachineCopying, ""
	f.svc.Blockers = []Blocker{
		BlockerFunc(func(context.Context) (string, error) { return copying, nil }),
		BlockerFunc(func(context.Context) (string, error) { return other, nil })}
	tick := func(action, reason string) {
		t.Helper()
		if d, err := f.svc.Tick(ctx); err != nil || d.Action != action || d.Reason != reason {
			t.Fatalf("at %s: %+v %v, want %s %s", f.now, d, err, action, reason)
		}
	}
	tick("blocked", BlockMachineCopying)
	f.now = f.now.Add(10 * time.Minute)
	// another blocker meanwhile does not begin the hold anew
	copying, other = "", BlockRoleNotRunning
	tick("blocked", BlockRoleNotRunning)
	copying, other = BlockMachineCopying, ""
	tick("blocked", BlockMachineCopying)
	// the next day's window
	f.now = f.now.Add(24*time.Hour - 20*time.Minute)
	other = BlockRoleNotRunning // what else holds it still does
	tick("blocked", BlockRoleNotRunning)
	other = ""
	audited := func(want int) {
		t.Helper()
		var n int
		if err := f.db.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'update_past_machine_copying'`).Scan(&n); err != nil || n != want {
			t.Fatalf("audited %d times (%v), want %d", n, err, want)
		}
	}
	audited(0)
	tick("started", "")
	audited(1)
}

// A copy that ended ends the hold: the next one is held anew.
func TestACopyThatEndedEndsItsHold(t *testing.T) {
	t.Parallel()
	enableAutoInstall(t)
	ctx := context.Background()
	f := newFixture(t, sqliteDB(t))
	copying := BlockMachineCopying
	f.svc.Blockers = []Blocker{BlockerFunc(func(context.Context) (string, error) { return copying, nil })}
	seed(t, f.store, rel("1.5.0"))
	if d, _ := f.svc.Tick(ctx); d.Reason != BlockMachineCopying {
		t.Fatalf("%+v", d)
	}
	copying = ""
	if d, _ := f.svc.Tick(ctx); d.Action != "started" {
		t.Fatalf("%+v", d)
	}
	if !f.svc.copySince.IsZero() || len(f.svc.pastCopies()) != 0 {
		t.Fatalf("the hold goes on: %s", f.svc.copySince)
	}
}

// A blocker that fails after one was gone past: no run starts, and nothing is audited.
func TestUpdateNowStopsAtABlockersErrorPastACopy(t *testing.T) {
	t.Parallel()
	enableAutoInstall(t)
	ctx := context.Background()
	f := newFixture(t, sqliteDB(t))
	seed(t, f.store, rel("1.5.0"))
	boom := errors.New("boom")
	f.svc.Blockers = []Blocker{
		BlockerFunc(func(context.Context) (string, error) { return BlockMachineCopying, nil }),
		BlockerFunc(func(context.Context) (string, error) { return "", boom })}
	if run, err := f.svc.RunNow(ctx, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ""); !errors.Is(err, boom) || run.ID != "" {
		t.Fatalf("%+v %v", run, err)
	}
	if run, _ := f.store.ActiveRun(ctx); run != nil {
		t.Fatalf("a run started: %+v", run)
	}
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM audit_log WHERE action LIKE 'update_past_%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("audited %d times (%v)", n, err)
	}
}

// The restart waits for a machine's copy, then goes at once; the run's other steps did not.
func TestDriverRestartWaitsForAMachinesCopy(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	copies := 2
	r.d.Copying = func(context.Context) int { return copies }
	if d, _ := r.f.svc.Tick(ctx); d.Action != "started" {
		t.Fatalf("%+v", d)
	}
	r.stepTo(t, 40, 10*time.Second, "the restart waits for the copy", func() bool {
		return r.runDetail(t).Waiting == WaitMachineCopying
	})
	if run := r.active(t); run.State != RunDraining || r.home.applies != 0 {
		t.Fatalf("%s, %d applies", run.State, r.home.applies)
	}
	copies = 0
	r.stepTo(t, 3, 10*time.Second, "the restart once the copy ended", func() bool { return r.home.applies == 1 })
	if det := r.runDetail(t); det.Waiting == WaitMachineCopying {
		t.Fatalf("still waiting: %+v", det)
	}
}

// Copies that never end: the restart goes on after CopyWait in all, an admin's run too, and
// says that it went past them.
func TestDriverRestartWaitsForMachinesCopiesTenMinutesInAll(t *testing.T) {
	t.Parallel()
	for _, trigger := range []string{TriggerAuto, TriggerManual} {
		t.Run(trigger, func(t *testing.T) {
			t.Parallel()
			r := newDriverRig(t)
			ctx := context.Background()
			copies := 1
			r.d.Copying = func(context.Context) int { return copies }
			if trigger == TriggerAuto {
				_, _ = r.f.svc.Tick(ctx)
			} else if _, err := r.f.svc.RunNow(ctx, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ""); err != nil {
				t.Fatal(err)
			}
			waits := func() bool { return r.runDetail(t).Waiting == WaitMachineCopying }
			r.stepTo(t, 40, 10*time.Second, "the restart waits", waits)
			since := *r.runDetail(t).CopySince
			r.stepTo(t, 120, 10*time.Second, "the restart past the wait", func() bool { return r.home.applies == 1 })
			if waited := r.f.now.Sub(since); waited < CopyWait || waited > CopyWait+30*time.Second {
				t.Fatalf("waited %s, want %s", waited, CopyWait)
			}
			if det := r.runDetail(t); det.Waiting != "" || !det.CopySince.Equal(since) {
				t.Fatalf("%+v", det)
			}
			var n int
			if err := r.f.db.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'copy_wait_over'`).Scan(&n); err != nil || n != 1 {
				t.Fatalf("audited %d times (%v)", n, err)
			}
		})
	}
}

// A home whose desktop app installs the release waits in the same place: its app's go.
func TestDriverHomeAppsGoWaitsForAMachinesCopy(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	r.home.asks = true
	copies := 1
	r.d.Copying = func(context.Context) int { return copies }
	_, _ = r.f.svc.Tick(context.Background())
	waits := func() bool { return r.runDetail(t).Waiting == WaitMachineCopying }
	r.stepTo(t, 40, 10*time.Second, "the go waits", waits)
	for i := 0; i < 5; i++ { // said at every step of the wait
		if r.stepN(1); !waits() {
			t.Fatalf("step %d of the wait: waiting %q", i, r.runDetail(t).Waiting)
		}
	}
	if run := r.active(t); run.State != RunHomeApplying || r.home.applies != 0 {
		t.Fatalf("%s, %d applies", run.State, r.home.applies)
	}
	copies = 0
	r.stepTo(t, 3, 10*time.Second, "the go once the copy ended", func() bool { return r.home.applies == 1 })
}
