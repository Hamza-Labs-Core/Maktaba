package main

import (
	"context"
	"log/slog"
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
