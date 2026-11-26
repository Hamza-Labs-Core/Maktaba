package updates

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
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

// #1753 item 2: a copy that held day 1's window and ended outside the window clears the
// hold so day 2's separate copy gets its own CopyHoldMax budget.
func TestHoldClearsOutsideWindowSoNextDayGetsFreshHold(t *testing.T) {
	t.Parallel()
	enableAutoInstall(t)
	ctx := context.Background()
	f := newFixture(t, sqliteDB(t))
	seed(t, f.store, rel("1.5.0"))
	copying := BlockMachineCopying
	f.svc.Blockers = []Blocker{BlockerFunc(func(context.Context) (string, error) { return copying, nil })}
	// Day 1 inside the quiet window (03:00-05:00): held by copy
	if d, err := f.svc.Tick(ctx); err != nil || d.Action != "blocked" || d.Reason != BlockMachineCopying {
		t.Fatalf("day 1 window: %+v %v", d, err)
	}
	// Window ends (06:00) and the copy finishes outside the window
	f.now = f.now.Add(3 * time.Hour)
	copying = ""
	if d, err := f.svc.Tick(ctx); err != nil || d.Action != "none" {
		t.Fatalf("outside window: %+v %v", d, err)
	}
	if !f.svc.copySince.IsZero() {
		t.Fatalf("copySince was not cleared outside the window: %s", f.svc.copySince)
	}
	// Day 2 inside the quiet window (24h after day 1's start): a new machine starts copying;
	// it must get a fresh hold instead of sharing day 1's expired 20h budget.
	f.now = f.now.Add(21 * time.Hour)
	copying = BlockMachineCopying
	if d, err := f.svc.Tick(ctx); err != nil || d.Action != "blocked" || d.Reason != BlockMachineCopying {
		t.Fatalf("day 2 new copy should be held anew: %+v %v", d, err)
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

// noTransport answers nothing.
type noTransport struct{}

func (noTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unreachable")
}

// That a run went past the machines' copies is audited when it starts, not at each try.
func TestPastACopyIsAuditedOnlyWhenTheRunStarts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newStaleRig(t, time.Hour)
	r.svc.Blockers = []Blocker{BlockerFunc(func(context.Context) (string, error) { return BlockMachineCopying, nil })}
	r.svc.copySince = r.now.Add(-CopyHoldMax)
	rows := func() (n int) {
		t.Helper()
		if err := r.db.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'update_past_machine_copying'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	fails := r.svc.Discoverer.HTTP
	r.svc.Discoverer.HTTP = &http.Client{Transport: noTransport{}}
	for range 3 {
		if d, err := r.svc.Tick(ctx); d.Action != "failed" || err == nil {
			t.Fatalf("the release could not be read again: %+v %v", d, err)
		}
	}
	if _, err := r.svc.RunNow(ctx, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ""); err == nil {
		t.Fatal("started with the release unread")
	}
	if n := rows(); n != 0 {
		t.Fatalf("%d rows of runs that did not start", n)
	}
	r.svc.Discoverer.HTTP = fails
	if d, err := r.svc.Tick(ctx); d.Action != "started" {
		t.Fatalf("%+v %v", d, err)
	}
	if n := rows(); n != 1 {
		t.Fatalf("%d rows of the run that started", n)
	}
}

// A copy the restart goes past after another wait is audited too, once: the TV watched from
// the middle of the copy's wait until past its end.
func TestDriverSaysItWentPastACopyAfterAnotherWait(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	r.d.Copying = func(context.Context) int { return 1 }
	_, _ = r.f.svc.Tick(context.Background())
	waiting := func(what string) func() bool {
		return func() bool { return r.runDetail(t).Waiting == what }
	}
	r.stepTo(t, 40, 10*time.Second, "the copy's wait", waiting(WaitMachineCopying))
	r.viewers = 3
	r.stepTo(t, 5, 10*time.Second, "the TV's wait", waiting("tv_busy"))
	r.f.now = r.f.now.Add(CopyWait)
	r.viewers = 0
	r.stepTo(t, 5, 10*time.Second, "the restart", func() bool { return r.home.applies == 1 })
	var n int
	var raw string
	if err := r.f.db.QueryRow(`SELECT count(*), COALESCE(max(payload), '') FROM audit_log WHERE action = 'copy_wait_over'`).Scan(&n, &raw); err != nil || n != 1 {
		t.Fatalf("audited %d times (%v)", n, err)
	}
	// #1753 item 5: waited_sec excludes the CopyWait (10 min) spent waiting for TV viewers
	var details struct {
		WaitedSec int `json:"waited_sec"`
		Copies    int `json:"copies"`
	}
	if err := json.Unmarshal([]byte(raw), &details); err != nil {
		t.Fatalf("audit details %q: %v", raw, err)
	}
	if details.Copies != 1 || details.WaitedSec >= int(CopyWait.Seconds()) {
		t.Fatalf("copy_wait_over details = %+v, want copies=1 and waited_sec excluding viewer wait (< %d)",
			details, int(CopyWait.Seconds()))
	}
}

// #1753 item 5: copy_wait_over is written only when the restart actually proceeds (apply
// succeeds), never if apply is refused (e.g. tv_busy) or fails first.
func TestCopyWaitOverWrittenOnlyWhenRestartProceeds(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	r.d.Copying = func(context.Context) int { return 1 }
	_, _ = r.f.svc.Tick(context.Background())
	r.stepTo(t, 40, 10*time.Second, "the copy's wait", func() bool {
		return r.runDetail(t).Waiting == WaitMachineCopying
	})
	// Advance past CopyWait, but make apply fail with tv_busy
	origHome := r.d.Home
	r.d.Home = applyRefusing{origHome, errors.New("tv_busy: viewers watching")}
	r.f.now = r.f.now.Add(CopyWait + time.Minute)
	r.stepN(3)
	var n int
	if err := r.f.db.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'copy_wait_over'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("copy_wait_over audited %d times before apply succeeded (%v)", n, err)
	}
	// Now apply succeeds: copy_wait_over is audited once
	r.d.Home = origHome
	r.stepTo(t, 5, 10*time.Second, "the restart", func() bool { return r.home.applies == 1 })
	if err := r.f.db.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'copy_wait_over'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("copy_wait_over audited %d times after apply succeeded (%v)", n, err)
	}
}

type applyRefusing struct {
	HomeSupervisor
	err error
}

func (h applyRefusing) Do(ctx context.Context, op string, body any) (*MachineStatus, error) {
	if op == "apply" {
		return nil, h.err
	}
	return h.HomeSupervisor.Do(ctx, op, body)
}

// What refuses a machine's action is its own code: only Update now has nothing to install.
func TestRefusedMachineActionsHaveTheirOwnCodes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newDriverRig(t)
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	run, err := r.f.store.InsertRunWith(ctx, "1.5.0", "1.4.0", TriggerManual, "admin", RunDetail{})
	if err != nil {
		t.Fatal(err)
	}
	answer := func(err error) (string, string) {
		t.Helper()
		w := httptest.NewRecorder()
		(&Handler{}).retryAnswer(w, httptest.NewRequest(http.MethodPost, "/", nil), Run{}, err)
		var body struct{ Code, Detail string }
		if w.Code != http.StatusConflict || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("%v: %d %s", err, w.Code, w.Body)
		}
		return body.Code, body.Detail
	}
	_, err = r.d.Retry(ctx, run.ID, []string{"n1"}, "admin")
	if code, detail := answer(err); code != "home_behind" || !strings.Contains(detail, "the home does not run 1.5.0") {
		t.Fatalf("a home behind: %q %q", code, detail)
	}
	if err := r.f.store.RaiseWatermark(ctx, "1.5.0"); err != nil {
		t.Fatal(err)
	}
	if err := r.f.store.SetWithdrawn(ctx, "1.5.0", WithdrawnGone); err != nil {
		t.Fatal(err)
	}
	_, err = r.d.Retry(ctx, run.ID, []string{"n1"}, "admin")
	if code, _ := answer(err); code != "release_withdrawn" {
		t.Fatalf("a withdrawn release: %q (%v)", code, err)
	}
	// #1753 item 4: 404 gone run carries code no_such_run
	{
		w := httptest.NewRecorder()
		(&Handler{}).retryAnswer(w, httptest.NewRequest(http.MethodPost, "/", nil), Run{}, ErrNoSuchRun)
		var body struct{ Code string }
		if w.Code != http.StatusNotFound || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Code != "no_such_run" {
			t.Fatalf("404 retryAnswer: %d %s", w.Code, w.Body)
		}
	}
	if err := r.f.store.InsertTarget(ctx, Target{RunID: run.ID, NodeID: "n1", ToVersion: "1.5.0"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name         string
		raw          string // a machine's answer (cmd/maktaba-server, updates.command), or its agent's
		err          error
		code, detail string
	}{
		{"not committed", `{"error":"no_rollback: the run is applying","status":{"state":"applying","journal":{"job":{"run_id":"r"}},"attempts":[]}}`,
			nil, "no_rollback", "the run is applying"},
		{"another run", `{"error":"update_no_run","status":{"attempts":[]}}`, nil, "update_no_run", ""},
		{"another reason", `{"error":"bad request: x","status":null}`, nil, "machine_refused", "bad request: x"},
		{"an old agent", `"the machine agent does not run update.rollback"`, nil, "machine_refused",
			"the machine agent does not run update.rollback"},
		{"the session ended", `"disconnected"`, nil, "machine_lost", "the connection to the machine was lost before it answered"},
		{"the session ended before it answered", ``, agent.ErrSessionLost, "machine_lost",
			"the connection to the machine was lost before it answered"},
		{"no session", ``, agent.ErrNoSession, "machine_offline", "the machine is not connected"},
		{"too long", ``, agent.ErrCmdTimeout, "machine_timeout", "the machine did not answer in time"},
	} {
		r.d.Cluster = answering{r.cluster, json.RawMessage(c.raw), c.err}
		_, err := r.d.TargetAction(ctx, run.ID, "n1", "rollback", "admin")
		if code, detail := answer(err); code != c.code || detail != c.detail {
			t.Errorf("%s: %q %q, want %q %q", c.name, code, detail, c.code, c.detail)
		}
	}
}

// answering is a cluster whose machines take no rollback.
type answering struct {
	*clusterFake
	raw json.RawMessage
	err error
}

func (c answering) Command(ctx context.Context, sid, name string, args any, d time.Duration) (bool, json.RawMessage, error) {
	if name == "update.rollback" {
		return false, c.raw, c.err
	}
	return c.clusterFake.Command(ctx, sid, name, args, d)
}

// A cancel the home did not take says past_switch only when the home said so.
func TestCancelSaysPastSwitchOnlyWhenTheHomeDoes(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.asks, r.home.appSlow = true, true
	_, _ = r.f.svc.Tick(ctx)
	r.stepN(5)
	run := r.active(t)
	if run.State != RunHomeApplying || r.home.state() != MOrdered {
		t.Fatalf("%s %s", run.State, r.home.state())
	}
	home := r.d.Home
	for said, want := range map[string]error{
		"supervisor unreachable: connection refused": ErrCancelFailed,
		"update_no_run":                    ErrCancelFailed,
		"past_switch: the run is applying": ErrPastSwitch,
	} {
		r.d.Home = refusing{home, errors.New(said)}
		if err := r.d.Cancel(ctx, run.ID, ""); !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", said, err, want)
		}
	}
	if got := r.active(t); got.State != RunHomeApplying {
		t.Fatalf("a cancel that was not taken left the run %s", got.State)
	}
	// it answers again: cancelled
	r.d.Home = home
	if err := r.d.Cancel(ctx, run.ID, ""); err != nil || r.home.state() != MAborted {
		t.Fatalf("%v %s", err, r.home.state())
	}
}

// refusing is a home that takes no abort.
type refusing struct {
	HomeSupervisor
	err error
}

func (h refusing) Do(ctx context.Context, op string, body any) (*MachineStatus, error) {
	if op == "abort" {
		return nil, h.err
	}
	return h.HomeSupervisor.Do(ctx, op, body)
}
