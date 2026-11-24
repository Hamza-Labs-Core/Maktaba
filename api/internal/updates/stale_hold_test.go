package updates

import (
	"context"
	"strings"
	"testing"
	"time"
)

// staleRig is a home with a catalog (1.5.0, verified) read from a fake GitHub, whose last
// good read is moved to ago before the window, and whose checks may fail.
type staleRig struct {
	*fixture
	g *fakeGitHub
}

func newStaleRig(t *testing.T, ago time.Duration) *staleRig {
	t.Helper()
	enableAutoInstall(t)
	r := &staleRig{fixture: newFixture(t, sqliteDB(t)), g: newFakeGitHub(t)}
	r.g.add("v1.5.0", false, true, "sums\n")
	r.svc.Discoverer = newDiscoverer(r.g, r.store, &fakeVerifier{}, r.notes, "1.4.0")
	r.svc.Discoverer.Now = func() time.Time { return r.now }
	if _, err := r.svc.Discoverer.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.state(func(st *State) {
		at := r.now.Add(-ago)
		st.LastCheckOK = &at
	})
	return r
}

func (r *staleRig) state(fn func(st *State)) {
	st := r.store.LoadState(context.Background())
	fn(&st)
	r.store.SaveState(context.Background(), st)
}

// down makes every check fail (or not).
func (r *staleRig) down(on bool) { r.g.set(func() { r.g.limited = on }) }

func (r *staleRig) tick(t *testing.T) Decision {
	t.Helper()
	d, err := r.svc.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (r *staleRig) held(t *testing.T, want bool, when string) {
	t.Helper()
	if got := contains(r.notes.kinds(), NoticeHeldStale); got != want {
		t.Fatalf("%s: told %v, want %v (%v)", when, got, want, r.notes.kinds())
	}
}

// The review of #1608: a home that slept 13 h (its ticker stops while it sleeps), with no
// failed check, and one a day's check apart, install in their window: the list is read again
// first, and nobody is told of anything. What it finds then is what installs.
func TestStaleListIsReadAgainBeforeItHolds(t *testing.T) {
	t.Parallel()
	r := newStaleRig(t, 13*time.Hour)
	r.g.add("v1.6.0", false, true, "newer\n")
	if d := r.tick(t); d.Action != "started" || d.Version != "1.6.0" {
		t.Fatalf("a home that slept: %+v", d)
	}
	r.held(t, false, "a home that slept")
	if st := r.store.LoadState(context.Background()); !st.LastCheckOK.Equal(r.now.UTC()) {
		t.Fatalf("not read again: %v", st.LastCheckOK)
	}
}

// Research B3 (2026-11-24): the home's checks had failed for a day, and its scheduler chose a
// release older than one that had rolled back. An automatic install waits while the list
// cannot be read; it is tried again every 15 minutes, not every minute, and goes once it is
// read. The admins are told once it lasts half an hour. Update now installs all the same.
func TestAutomaticInstallsWaitWhileTheListCannotBeRead(t *testing.T) {
	t.Parallel()
	r := newStaleRig(t, 13*time.Hour)
	r.down(true)
	asked := func() time.Time { return r.svc.staleChecked }
	if d := r.tick(t); d.Action != "none" || d.Reason != WaitCatalogStale || d.Version != "1.5.0" || len(r.starter.runs) != 0 {
		t.Fatalf("a list that cannot be read: %+v starts=%v", d, r.starter.runs)
	}
	if lt := r.svc.LastTick(); lt == nil || lt.Reason != WaitCatalogStale {
		t.Fatalf("the Updates page's last tick: %+v", lt)
	}
	first := asked()
	for range 14 {
		r.now = r.now.Add(time.Minute)
		_ = r.tick(t)
	}
	if !asked().Equal(first) {
		t.Fatal("read again within 15 minutes")
	}
	r.held(t, false, "held 14 minutes")
	for range 17 {
		r.now = r.now.Add(time.Minute)
		_ = r.tick(t)
	}
	if asked().Equal(first) {
		t.Fatal("never read again")
	}
	r.held(t, true, "held half an hour")
	if st := r.store.LoadState(context.Background()); st.Failures != 0 || contains(r.notes.kinds(), NoticeCheckFailing) {
		t.Fatalf("its reads counted as failing checks: %+v %v", st, r.notes.kinds())
	}
	// an admin's Update now installs all the same
	u := newStaleRig(t, 13*time.Hour)
	u.down(true)
	_ = u.tick(t)
	u.down(false)
	if run, err := u.svc.RunNow(context.Background(), "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ""); err != nil || run.Version != "1.5.0" {
		t.Fatalf("update now on a stale list: %+v %v", run, err)
	}
	// the list can be read: the next try installs
	r.down(false)
	r.svc.Discoverer.retryAfter = time.Time{}
	r.now = r.now.Add(15 * time.Minute)
	if d := r.tick(t); d.Action != "started" {
		t.Fatalf("once it is read: %+v", d)
	}
}

// A start before the network is up: its check fails, the next is 6 h away. The install waits
// for the network, not for that, and nobody is told.
func TestStartBeforeTheNetworkInstallsOnceItIsUp(t *testing.T) {
	t.Parallel()
	r := newStaleRig(t, 20*time.Hour)
	r.down(true)
	if _, err := r.svc.Discoverer.Check(context.Background()); err == nil {
		t.Fatal("the start's check passed")
	}
	if d := r.tick(t); d.Reason != WaitCatalogStale {
		t.Fatalf("before the network: %+v", d)
	}
	r.down(false)
	r.svc.Discoverer.retryAfter = time.Time{}
	r.now = r.now.Add(16 * time.Minute)
	if d := r.tick(t); d.Action != "started" {
		t.Fatalf("once it is up: %+v", d)
	}
	r.held(t, false, "a start before the network")
}

// The checks keep failing (two in a row, as the Updates page says): the admins are told of
// the hold at once.
func TestHeldWhileTheChecksKeepFailingIsSaidAtOnce(t *testing.T) {
	t.Parallel()
	r := newStaleRig(t, 30*time.Hour)
	r.down(true)
	r.state(func(st *State) { st.Failures, st.LastCheckErr = 4, "HTTP 500" })
	_ = r.tick(t)
	r.held(t, true, "four failed checks")
}

// The cadence scales it: MAKTABA_UPDATE_INTERVAL_SEC=86400 (docs/updates.md), a home started
// at noon: its list is 15 h old every night at 3, and it is not read again nor held. Two days
// old it is read again. With the background checks off (0), what an admin's check found
// installs.
func TestStaleHoldFollowsTheCadence(t *testing.T) {
	t.Parallel()
	day, off := 24*time.Hour, time.Duration(0)
	for name, c := range map[string]struct {
		every *time.Duration
		ago   time.Duration
		// the list is read again before the install
		reread bool
	}{
		"a day's cadence, 15 h":   {&day, 15 * time.Hour, false},
		"a day's cadence, 2 days": {&day, 49 * time.Hour, true},
		"no background checks":    {&off, 60 * day, false},
		"the default, 11 h":       {nil, 11 * time.Hour, false},
		"the default, 13 h":       {nil, 13 * time.Hour, true},
	} {
		t.Run(name, func(t *testing.T) {
			r := newStaleRig(t, c.ago)
			r.svc.CheckEvery = c.every
			d, err := r.svc.Tick(context.Background())
			if err != nil || d.Action != "started" {
				t.Fatalf("%+v %v", d, err)
			}
			if asked := !r.svc.staleChecked.IsZero(); asked != c.reread {
				t.Fatalf("read again %v, want %v", asked, c.reread)
			}
		})
	}
}

// A good read saved at a time still to come (the clock was a year ahead then) says nothing of
// its age: the list is read again, and the install waits while it cannot be.
func TestAReadInTheFutureIsNotFresh(t *testing.T) {
	t.Parallel()
	r := newStaleRig(t, -365*24*time.Hour)
	r.down(true)
	r.state(func(st *State) { st.Failures, st.LastCheckErr = 40, "HTTP 500" })
	if d := r.tick(t); d.Reason != WaitCatalogStale {
		t.Fatalf("a year ahead, 40 failed checks: %+v", d)
	}
	r.down(false)
	r.svc.Discoverer.retryAfter = time.Time{}
	r.now = r.now.Add(16 * time.Minute)
	if d := r.tick(t); d.Action != "started" {
		t.Fatalf("read again: %+v", d)
	}
}

// An Update now of the version under way is what holds the scheduler, not the list: nothing is
// read again, nobody is told it waits.
func TestNoHoldWhileARunIsUnderWay(t *testing.T) {
	t.Parallel()
	r := newStaleRig(t, 13*time.Hour)
	if _, err := r.svc.RunNow(context.Background(), "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ""); err != nil {
		t.Fatal(err)
	}
	r.state(func(st *State) {
		at := r.now.Add(-13 * time.Hour)
		st.LastCheckOK, st.Failures = &at, 4
	})
	r.down(true)
	if d := r.tick(t); d.Action != "blocked" || d.Reason != BlockActiveRun {
		t.Fatalf("a run under way: %+v", d)
	}
	r.held(t, false, "a run under way")
	if !r.svc.staleChecked.IsZero() {
		t.Fatal("the list was read again")
	}
}

// The read again waits for a check that runs (the start's own), and does not read what that
// one just read.
func TestRereadSkipsWhatACheckJustRead(t *testing.T) {
	t.Parallel()
	r := newStaleRig(t, 13*time.Hour)
	if _, err := r.svc.Discoverer.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.down(true)
	if !r.svc.rereadList(context.Background()) {
		t.Fatal("a list just read was read again, or is stale")
	}
}

func TestCatalogStale(t *testing.T) {
	now := time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	for _, c := range []struct {
		st    State
		every time.Duration
		stale bool
	}{
		{State{}, CheckInterval, false},
		{State{LastCheckOK: at(time.Hour)}, CheckInterval, false},
		{State{LastCheckOK: at(-time.Minute)}, CheckInterval, false},
		{State{LastCheckOK: at(-time.Hour)}, CheckInterval, true},
		{State{LastCheckOK: at(StaleHoldAfter - time.Minute), LastCheckErr: "x"}, CheckInterval, false},
		{State{LastCheckOK: at(StaleHoldAfter + time.Minute)}, CheckInterval, true},
		{State{LastCheckOK: at(StaleHoldAfter + time.Minute)}, time.Hour, true},
		{State{LastCheckOK: at(StaleHoldAfter + time.Minute)}, 24 * time.Hour, false},
		{State{LastCheckErr: "HTTP 500"}, CheckInterval, true},
		{State{LastCheckErr: "HTTP 500"}, 0, false},
	} {
		why := catalogStale(c.st, now, c.every)
		if (why != "") != c.stale {
			t.Errorf("%+v: %q", c.st, why)
		}
		if c.stale && !strings.Contains(why, "list of releases") {
			t.Errorf("why %q", why)
		}
	}
}
