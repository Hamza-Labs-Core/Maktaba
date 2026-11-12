package parental_test

// Review of the filter, findings 5 and 9, against a real migrated Postgres: what a
// limit that cannot be read means, and how long a kept entry is trusted.

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// Finding 5: when the policies cannot be read, a sign-in whose last answer (a short while
// ago) was "no limit at all" is answered that again; an account with a policy, a sign-in
// with a device limit, and one never read are refused. A change that was heard, and time,
// end the answer.
func TestCacheWhenThePolicyCannotBeRead(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.restrict(uChild)
	open := func() (*parental.Cache, *sql.DB, *testClock) {
		t.Helper()
		db, err := sql.Open("postgres", e.dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		clock := &testClock{at: time.Now()}
		c := parental.NewCache(parental.NewStore(db))
		c.SetClock(clock.now)
		return c, db, clock
	}
	nine := parental.Tier9
	viewer := &principal.Principal{UserID: uViewer}
	viewerOnDevice := &principal.Principal{UserID: uViewer, AgeCeiling: &nine}
	child := &principal.Principal{UserID: uChild}
	other := &principal.Principal{UserID: uGuardian} // never read before the database went

	c, db, clock := open()
	for _, p := range []*principal.Principal{viewer, viewerOnDevice, child} {
		if _, err := c.For(ctx, p); err != nil {
			t.Fatalf("read %s: %v", p.UserID, err)
		}
	}
	if eff, _ := c.For(ctx, viewerOnDevice); !eff.Limits() {
		t.Fatal("the device's limit does not hold the viewer's sign-in")
	}
	_ = db.Close() // the home's database is gone

	if eff, err := c.For(ctx, viewer); err != nil || eff.Limits() {
		t.Errorf("an unrestricted account read a moment ago: %+v, %v; want no limit", eff, err)
	}
	if eff, err := c.For(ctx, child); err == nil {
		t.Errorf("a restricted account was answered %+v with the database gone; want an error", eff)
	}
	if eff, err := c.For(ctx, viewerOnDevice); err == nil {
		t.Errorf("a sign-in with a device limit was answered %+v with the database gone; want an error", eff)
	}
	if eff, err := c.For(ctx, other); err == nil {
		t.Errorf("an account never read was answered %+v with the database gone; want an error", eff)
	}
	// the answer is bounded in time
	clock.add(parental.UnlimitedFor - time.Second)
	if _, err := c.For(ctx, viewer); err != nil {
		t.Errorf("just inside the bound: %v", err)
	}
	clock.add(2 * time.Second)
	if eff, err := c.For(ctx, viewer); err == nil {
		t.Errorf("past the bound the unrestricted account was answered %+v; want an error", eff)
	}
	if parental.UnlimitedFor > 5*time.Minute {
		t.Errorf("the bound is %s, want a few minutes at most", parental.UnlimitedFor)
	}

	// a change that was heard ends it at once
	c, db, _ = open()
	if _, err := c.For(ctx, viewer); err != nil {
		t.Fatal(err)
	}
	c.Invalidate(uViewer)
	_ = db.Close()
	if eff, err := c.For(ctx, viewer); err == nil {
		t.Errorf("after a change to the account was heard it was answered %+v; want an error", eff)
	}

	// so does a listener that connects again (what changed meanwhile is unknown); one
	// that is lost does not (that is the outage the answer is for)
	c, db, _ = open()
	if _, err := c.For(ctx, viewer); err != nil {
		t.Fatal(err)
	}
	c.Suspend()
	_ = db.Close()
	if _, err := c.For(ctx, viewer); err != nil {
		t.Errorf("after the listener was lost: %v", err)
	}
	c.Resume()
	c.Suspend()
	if eff, err := c.For(ctx, viewer); err == nil {
		t.Errorf("after the listener connected again it was answered %+v; want an error", eff)
	}

	// an account that got a policy while it was remembered as unlimited: the read that
	// works replaces the answer
	c, db, _ = open()
	if _, err := c.For(ctx, &principal.Principal{UserID: uChild2}); err != nil {
		t.Fatal(err)
	}
	e.restrict(uChild2)
	if eff, err := c.For(ctx, &principal.Principal{UserID: uChild2}); err != nil || !eff.Limits() {
		t.Fatalf("the new policy: %+v, %v", eff, err)
	}
	_ = db.Close()
	if eff, err := c.For(ctx, &principal.Principal{UserID: uChild2}); err == nil {
		t.Errorf("a now restricted account was answered %+v with the database gone; want an error", eff)
	}
}

// The read is bounded: a database that does not answer does not hold the request.
func TestCacheReadIsBounded(t *testing.T) {
	e := setup(t)
	db, err := sql.Open("postgres", e.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background()) // the one connection is taken: every read waits
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	c := parental.NewCache(parental.NewStore(db))
	start := time.Now()
	_, err = c.For(context.Background(), &principal.Principal{UserID: uViewer})
	if took := time.Since(start); err == nil || took > parental.ReadTimeout+2*time.Second || took < parental.ReadTimeout/2 {
		t.Errorf("a read that cannot be answered: %v after %s; want an error after about %s", err, took, parental.ReadTimeout)
	}
}

// Finding 9: no entry is trusted for longer than its maximum age, even when no change
// was heard.
func TestCacheEntriesHaveAMaximumAge(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.restrict(uChild)
	clock := &testClock{at: time.Now()}
	c := parental.NewCache(e.store)
	c.SetClock(clock.now)
	c.Resume() // as a connected listener: entries are kept
	tier := func() int {
		t.Helper()
		eff, err := c.For(ctx, &principal.Principal{UserID: uChild})
		if err != nil {
			t.Fatal(err)
		}
		return eff.MaxTier
	}
	if got := tier(); got != parental.Tier9 {
		t.Fatalf("first read: %d", got)
	}
	gen := c.Generation()
	// a change this cache never hears of (its listener's connection died quietly)
	e.exec(`UPDATE parental_policies SET max_tier = 0 WHERE user_id = $1`, uChild)
	if got := tier(); got != parental.Tier9 {
		t.Fatalf("the entry was not kept (%d): the test cannot tell an aged entry from none", got)
	}
	clock.add(parental.EntryMaxAge - time.Second)
	if got := tier(); got != parental.Tier9 {
		t.Errorf("inside its maximum age the entry was read again (%d)", got)
	}
	clock.add(2 * time.Second)
	if got := tier(); got != parental.TierAll {
		t.Errorf("past its maximum age the entry still answers %d, want the tightened 0", got)
	}
	if c.Generation() == gen {
		t.Error("the generation did not change when the entries aged out: a feed that kept its own answer would not ask again")
	}
	if parental.EntryMaxAge > 2*time.Minute {
		t.Errorf("the maximum age is %s, want a short one", parental.EntryMaxAge)
	}
}
