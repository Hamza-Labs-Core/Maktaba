package liveguard

import (
	"context"
	"net/netip"
	"testing"
	"time"
)

// Review finding 9: the list is loaded with a context of its own. A client that
// hangs up while its request is the one loading does not empty the list for
// everyone (which refused Plex's tuner for two seconds).
func TestTunerLoadOutlivesTheRequest(t *testing.T) {
	clk := &clock{t: t0}
	plex := netip.MustParseAddr("10.0.0.7")
	tn := &Tuner{Now: clk.now, Load: func(ctx context.Context) ([]netip.Prefix, error) {
		if err := ctx.Err(); err != nil {
			return nil, err // what a database read answers on a cancelled context
		}
		return []netip.Prefix{netip.PrefixFrom(plex, 32)}, nil
	}}
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if !tn.Allowed(gone, plex) {
		t.Fatal("the load was cancelled with the request that started it")
	}
	if !tn.Allowed(context.Background(), plex) {
		t.Fatal("a request that hung up emptied the list for the next one")
	}
}

// A slow reload holds up the one request doing it. The others are answered from the
// list as it stands, and the first load of all is waited for, never guessed.
func TestTunerSlowReloadStallsNoOneElse(t *testing.T) {
	clk := &clock{t: t0}
	plex, other := netip.MustParseAddr("10.0.0.7"), netip.MustParseAddr("10.0.0.8")
	started, release := make(chan struct{}, 8), make(chan struct{})
	slow := false
	tn := &Tuner{Now: clk.now, Load: func(context.Context) ([]netip.Prefix, error) {
		if slow {
			started <- struct{}{}
			<-release
		}
		return []netip.Prefix{netip.PrefixFrom(plex, 32)}, nil
	}}
	ctx := context.Background()
	answer := func(addr netip.Addr) chan bool {
		ch := make(chan bool, 1)
		go func() { ch <- tn.Allowed(ctx, addr) }()
		return ch
	}
	within := func(what string, ch chan bool, want bool) {
		t.Helper()
		select {
		case got := <-ch:
			if got != want {
				t.Fatalf("%s = %v, want %v", what, got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: stalled behind another request's load", what)
		}
	}

	// the first load of all: everyone waits for it
	slow = true
	first := answer(plex)
	<-started
	second := answer(plex)
	select {
	case got := <-second:
		t.Fatalf("answered %v before any list was loaded", got)
	case <-time.After(100 * time.Millisecond):
	}
	// a request that hangs up while waiting is refused, not answered from nothing
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if tn.Allowed(gone, plex) {
		t.Fatal("allowed with no list loaded")
	}
	release <- struct{}{}
	within("the loading request", first, true)
	within("the waiting request", second, true)

	// the list is due again and its reload is slow
	clk.add(TunerTTL + time.Second)
	reloading := answer(plex)
	<-started
	within("Plex during a slow reload", answer(plex), true)
	within("a stranger during a slow reload", answer(other), false)
	release <- struct{}{}
	within("the reloading request", reloading, true)
}
