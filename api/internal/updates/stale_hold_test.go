package updates

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Research B3 (2026-11-24): the home's checks had failed for a day, and its scheduler
// chose a release older than one that had rolled back, from a list that no longer knew the
// newest releases. Automatic installs wait while the last good read of the list is older
// than StaleHoldAfter (or every check so far failed), and start again once a check reads
// it; an admin's Update now still installs; a home that never checked is not held.
func TestAutomaticInstallsWaitForAFreshListOfReleases(t *testing.T) {
	t.Parallel()
	enableAutoInstall(t)
	ctx := context.Background()
	f := newFixture(t, sqliteDB(t))
	seed(t, f.store, rel("1.5.0"))
	old := f.now.Add(-StaleHoldAfter - time.Minute)
	f.store.SaveState(ctx, State{LastCheckOK: &old, LastCheckErr: "GET …/releases: larger than 8388608 bytes"})
	d, err := f.svc.Tick(ctx)
	if err != nil || d.Action != "none" || d.Reason != WaitCatalogStale || d.Version != "1.5.0" || len(f.starter.runs) != 0 {
		t.Fatalf("a stale list: %+v %v starts=%v", d, err, f.starter.runs)
	}
	if lt := f.svc.LastTick(); lt == nil || lt.Reason != WaitCatalogStale {
		t.Fatalf("the Updates page's last tick: %+v", lt)
	}
	notes := f.notes.kinds()
	if !contains(notes, NoticeHeldStale) {
		t.Fatalf("no %s notice: %v", NoticeHeldStale, notes)
	}
	// every check so far failed: held too
	f.store.SaveState(ctx, State{LastCheckErr: "dial tcp: no route to host"})
	if d, _ := f.svc.Tick(ctx); d.Reason != WaitCatalogStale {
		t.Fatalf("never read: %+v", d)
	}
	// an admin's Update now installs all the same
	if run, err := f.svc.RunNow(ctx, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ""); err != nil || run.Version != "1.5.0" {
		t.Fatalf("update now on a stale list: %+v %v", run, err)
	}

	// a fresh read: the automatic install starts
	g := newFixture(t, sqliteDB(t))
	seed(t, g.store, rel("1.5.0"))
	fresh := g.now.Add(-time.Hour)
	g.store.SaveState(ctx, State{LastCheckOK: &fresh})
	if d, err := g.svc.Tick(ctx); err != nil || d.Action != "started" {
		t.Fatalf("a fresh list: %+v %v", d, err)
	}
}

func TestCatalogStale(t *testing.T) {
	now := time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	for _, c := range []struct {
		st    State
		stale bool
	}{
		{State{}, false},
		{State{LastCheckOK: at(time.Hour)}, false},
		{State{LastCheckOK: at(StaleHoldAfter - time.Minute), LastCheckErr: "x"}, false},
		{State{LastCheckOK: at(StaleHoldAfter + time.Minute)}, true},
		{State{LastCheckErr: "HTTP 500"}, true},
	} {
		why := catalogStale(c.st, now)
		if (why != "") != c.stale {
			t.Errorf("%+v: %q", c.st, why)
		}
		if c.stale && !strings.Contains(why, "list of releases") {
			t.Errorf("why %q", why)
		}
	}
}
