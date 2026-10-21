package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/nodeauth"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/nodemode"
)

type beatCopy struct{ synced time.Time }

func (beatCopy) Introspect(context.Context, delegation.IntrospectRequest) (*delegation.IntrospectAnswer, error) {
	return nil, nodeauth.ErrNotLive
}
func (beatCopy) Principal(context.Context, string) (*delegation.Principal, error) {
	return nil, nodeauth.ErrNotLive
}
func (beatCopy) SignIn(context.Context, string, string) (*delegation.Principal, error) {
	return nil, nodeauth.ErrNotLive
}
func (c beatCopy) Synced(context.Context) (time.Time, error) { return c.synced, nil }

// #157: with the home there, the node reads from its copy only while the copy is within
// rules.cluster.copy_reads_max_lag_seconds of it; further behind, its reads go to the
// home's database (a change of rights or a removal is not read stale).
func TestReadsGoToTheHomeWhileTheCopyIsBehind(t *testing.T) {
	cp := &beatCopy{synced: time.Now().Add(-5 * time.Second)}
	rt := &nodeRuntime{logger: slog.Default(), resolver: &nodeauth.Resolver{Local: cp},
		rules: func() nodemode.Rules { r := nodemode.DefaultRules(); r.CopyReadsMaxLagSeconds = 30; return r }}
	if !rt.copyFresh(context.Background()) {
		t.Fatal("a copy 5 s behind is not read from")
	}
	cp.synced = time.Now().Add(-time.Minute)
	rt.fresh.at = time.Time{} // the next check reads the beat again
	if rt.copyFresh(context.Background()) {
		t.Fatal("a copy a minute behind is read from")
	}
	if rt.fresh.lag < 59*time.Second {
		t.Fatalf("the lag: %s", rt.fresh.lag)
	}
	// no copy: never
	rt2 := &nodeRuntime{logger: slog.Default(), resolver: &nodeauth.Resolver{}, rules: nodemode.DefaultRules}
	if rt2.copyFresh(context.Background()) {
		t.Fatal("no copy is read from")
	}
}

// The copy's beat is stamped on the home's clock: its lag is measured on it, not on
// this machine's (a clock an hour off neither stops copy reads nor allows stale ones).
func TestTheCopysLagIsOnTheHomesClock(t *testing.T) {
	homeNow := time.Now().Add(time.Hour) // the home's clock runs an hour ahead
	cp := &beatCopy{synced: homeNow.Add(-5 * time.Second)}
	rt := &nodeRuntime{logger: slog.Default(), resolver: &nodeauth.Resolver{Local: cp},
		rules: func() nodemode.Rules { r := nodemode.DefaultRules(); r.CopyReadsMaxLagSeconds = 30; return r }}
	rt.clock.offset, rt.clock.at = time.Hour, time.Now()
	if !rt.copyFresh(context.Background()) || rt.fresh.lag > 10*time.Second {
		t.Fatalf("a copy 5 s behind the home: fresh %v, lag %s", rt.fresh.fresh, rt.fresh.lag)
	}
	// an hour behind on the home's clock (current on this machine's): stale
	cp.synced = time.Now()
	rt.fresh.at = time.Time{}
	if rt.copyFresh(context.Background()) {
		t.Fatal("a copy an hour behind the home is read from")
	}
}

// Read-your-writes: a change sends its author's reads to the home until the copy holds a
// beat after it, however far behind the copy is; others read the copy.
func TestAChangeIsReadFromTheHomeUntilTheCopyHasIt(t *testing.T) {
	now := time.Now()
	rt := &nodeRuntime{logger: slog.Default()}
	rt.recent.Now = func() time.Time { return now }
	rt.recent.Wrote("author")
	if !rt.recent.Changed("author", now.Add(-time.Minute)) {
		t.Fatal("the author reads a copy that does not have their change")
	}
	if rt.recent.Changed("someone else", now.Add(-time.Minute)) {
		t.Fatal("someone else is sent to the home")
	}
	// 20 s later the copy still lags (no beat after the change): still the home
	now = now.Add(20 * time.Second)
	if !rt.recent.Changed("author", now.Add(-25*time.Second)) {
		t.Fatal("a copy 25 s behind is read before it has the change")
	}
	// a beat after the change: the copy has it
	if rt.recent.Changed("author", now.Add(-18*time.Second)) {
		t.Fatal("the author is kept on the home after the copy has the change")
	}
}

// /api/cluster/self is public: the copy's state and progress, never its detail (an
// error names roles and paths).
func TestTheCopysPublicStatus(t *testing.T) {
	raw := []byte(`{"state":"error","detail":"pq: role \"maktaba_rep_x\" at C:\\Users\\me\\keys","lag_ms":1200,
	  "errors":3,"local_schema":144,"checked":"2026-10-21T10:00:00Z","applied_at":"2026-10-21T09:59:58Z"}`)
	got := string(publicCopy(raw))
	if strings.Contains(got, "detail") || strings.Contains(got, "maktaba_rep") || strings.Contains(got, "local_schema") ||
		!strings.Contains(got, `"state":"error"`) || !strings.Contains(got, `"lag_ms":1200`) ||
		!strings.Contains(got, "applied_at") {
		t.Fatalf("public copy status: %s", got)
	}
	if publicCopy([]byte("not json")) != nil {
		t.Fatal("garbage shown")
	}
}
