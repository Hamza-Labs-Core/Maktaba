package parental_test

// The review's findings on the time keeper, against a real migrated Postgres.

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// Lifting a restriction tells streaming at once that the account is no longer kept
// (everything it holds for the account goes), and the keeper forgets its marks: a
// library the child was once refused in would otherwise refuse the account's new
// stream URLs until streaming restarts.
func TestLiftClearsWhatStreamingHolds(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 60, ""))
	v := e.video(libOpen)
	e.post(0, e.session(uChild, v), v)

	if err := e.store.Lift(ctx, uAdmin, uChild); err != nil {
		t.Fatal(err)
	}
	if err := e.keeper.KeptChanged(ctx, uChild); err != nil {
		t.Fatalf("KeptChanged: %v", err)
	}
	if want := []string{uChild + "=false"}; !slices.Equal(e.push.keptSaid, want) {
		t.Fatalf("streaming was told %v, want %v", e.push.keptSaid, want)
	}
	// Refresh says the same for an account that is no longer kept, whatever
	// libraries it is asked about
	e.push.keptSaid = nil
	if err := e.keeper.Refresh(ctx, uChild); err != nil {
		t.Fatal(err)
	}
	if want := []string{uChild + "=false"}; !slices.Equal(e.push.keptSaid, want) {
		t.Fatalf("Refresh told streaming %v, want %v", e.push.keptSaid, want)
	}
	// streaming not answering is an error the caller sees
	e.push.fail = context.DeadlineExceeded
	if err := e.keeper.KeptChanged(ctx, uChild); err == nil {
		t.Fatal("a push streaming did not take was reported as done")
	}
}

// Removing every time rule drops the end streaming holds for the account in every
// library, also one no session is playing in.
func TestNoRulesLeftClearsEveryEnd(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 60, ""))
	if err := e.keeper.Refresh(ctx, uChild); err != nil {
		t.Fatal(err)
	}
	if len(e.push.clearedAll) != 0 {
		t.Fatalf("an account with a rule had its ends cleared: %v", e.push.clearedAll)
	}
	e.rules()
	if err := e.keeper.Refresh(ctx, uChild); err != nil {
		t.Fatal(err)
	}
	if want := []string{uChild}; !slices.Equal(e.push.clearedAll, want) {
		t.Fatalf("cleared for %v, want %v", e.push.clearedAll, want)
	}
	if len(e.push.keptSaid) != 0 {
		t.Fatalf("a restricted account was said to be %v", e.push.keptSaid)
	}
}

// Making an account restricted tells streaming it is kept, so a stream URL signed
// before that (it names no session) is refused.
func TestRestrictingTellsStreaming(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.restrict(uViewer)
	if err := e.keeper.KeptChanged(ctx, uViewer); err != nil {
		t.Fatal(err)
	}
	if want := []string{uViewer + "=true"}; !slices.Equal(e.push.keptSaid, want) {
		t.Fatalf("streaming was told %v, want %v", e.push.keptSaid, want)
	}
}

// A progress post that finds no mark (the API restarted) counts one heartbeat at
// most, never the whole session since it opened: that was counted already.
func TestRebuiltMarkCountsOneHeartbeatAtMost(t *testing.T) {
	e := setupTime(t)
	e.rules(limit(everyDay, 600, ""))
	v := e.video(libOpen)
	s := e.session(uChild, v)
	e.post(0, s, v)
	for i := 0; i < 80; i++ { // 80 minutes of a film, a post every 30 s
		e.post(30*time.Second, s, v)
		e.post(30*time.Second, s, v)
	}
	if got := e.used(libOpen); got != 80*60 {
		t.Fatalf("80 minutes played: %d s counted", got)
	}
	// the API restarts: a new keeper with no marks
	e.keeper = parental.NewTimeKeeper(parental.NewCache(e.store), e.push)
	e.post(30*time.Second, s, v)
	if got := e.used(libOpen) - 80*60; got < 30 || got > int(parental.HeartbeatCap/time.Second) {
		t.Fatalf("the first post after a restart added %d s, want 30 to 60", got)
	}
	before := e.used(libOpen)
	e.post(30*time.Second, s, v)
	if got := e.used(libOpen) - before; got != 30 {
		t.Fatalf("the next post added %d s, want 30", got)
	}
}

// A progress post after the session closed is not counted again and gives no lease.
func TestClosedSessionGetsNoMarkAndNoLease(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 600, ""))
	v := e.video(libOpen)
	s := e.session(uChild, v)
	e.post(0, s, v)
	e.post(30*time.Second, s, v)
	if err := e.keeper.Closed(ctx, s); err != nil {
		t.Fatal(err)
	}
	e.exec(`UPDATE streaming_sessions SET closed_at = $2 WHERE id = $1::uuid`, s, e.clock)
	used, pushes := e.used(libOpen), len(e.push.got)
	ended := e.push.lease(s)
	if ended == nil || ended.After(e.clock) {
		t.Fatalf("the close left the lease at %v", ended)
	}
	e.clock = e.clock.Add(40 * time.Minute)
	st, err := e.keeper.Beat(ctx, uChild, s)
	if err != nil || !st.Allowed {
		t.Fatalf("Beat on a closed session = %+v, %v", st, err)
	}
	if got := e.used(libOpen); got != used {
		t.Fatalf("a post after the close counted %d s more", got-used)
	}
	if len(e.push.got) != pushes {
		t.Fatalf("a post after the close pushed %+v", e.push.got[pushes:])
	}
	if l := e.push.lease(s); l == nil || !l.Equal(*ended) {
		t.Fatalf("a closed session's lease moved to %v", l)
	}
}

// Calls for one session run together (two progress posts, and another session's
// call that settles the account): under -race this reads no mark outside the lock.
func TestConcurrentCallsForOneSession(t *testing.T) {
	e := setupTime(t)
	ctx := context.Background()
	e.rules(limit(everyDay, 600, ""))
	v := e.video(libOpen)
	s1, s2 := e.session(uChild, v), e.session(uChild, v)
	var clock sync.Mutex
	now := e.clock
	e.store.Now = func() time.Time {
		clock.Lock()
		defer clock.Unlock()
		now = now.Add(25 * time.Second) // gaps past the heartbeat interval too
		return now
	}
	for _, s := range []string{s1, s2} {
		if err := e.keeper.Opened(ctx, uChild, libOpen, v, s); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				switch i % 4 {
				case 0, 1:
					_, _ = e.keeper.Beat(ctx, uChild, s1)
				case 2:
					_, _ = e.keeper.Beat(ctx, uChild, s2) // settles s1
				default:
					_, _ = e.keeper.Admit(ctx, e.child, libOpen)
					_ = e.keeper.Opened(ctx, uChild, libOpen, v, s1)
				}
			}
		}(i)
	}
	wg.Wait()
	if e.used(libOpen) == 0 {
		t.Fatal("nothing was counted")
	}
}
