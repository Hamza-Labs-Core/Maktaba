package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// idleConn is a connection that never reads a frame until it is closed.
type idleConn struct {
	once sync.Once
	done chan struct{}
}

func newIdleConn() *idleConn { return &idleConn{done: make(chan struct{})} }

func (c *idleConn) ReadFrame() (Frame, error) { <-c.done; return Frame{}, errors.New("closed") }
func (c *idleConn) WriteFrame(Frame) error    { return nil }
func (c *idleConn) Close() error              { c.once.Do(func() { close(c.done) }); return nil }

// countingStanding counts its queries, how many ran at once and the most ids one asked for.
type countingStanding struct {
	revoked sync.Map // server ids with no standing
	queries atomic.Int64
	running atomic.Int64
	atOnce  atomic.Int64
	biggest atomic.Int64
}

func (s *countingStanding) Standing(_ context.Context, ids []string) (map[string]bool, error) {
	s.queries.Add(1)
	if n := s.running.Add(1); n > s.atOnce.Load() {
		s.atOnce.Store(n)
	}
	defer s.running.Add(-1)
	if n := int64(len(ids)); n > s.biggest.Load() {
		s.biggest.Store(n)
	}
	time.Sleep(20 * time.Millisecond) // a slow database: what comes meanwhile is one batch
	out := map[string]bool{}
	for _, id := range ids {
		if _, gone := s.revoked.Load(id); !gone {
			out[id] = true
		}
	}
	return out, nil
}

// A storm: 3,000 homes connect and 30,000 notifications arrive at once. The checks are a few
// queries, one at a time, none over the batch; the homes without standing go, the others stay.
func TestRevokerStormIsAFewQueriesOneAtATime(t *testing.T) {
	reg := NewRegistry()
	st := &countingStanding{}
	rev := &Revoker{Registry: reg, Store: st, Logger: slog.New(slog.DiscardHandler), Every: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rev.Run(ctx)

	const homes = 3000
	tunnels := make([]*Tunnel, homes)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := g; i < homes; i += 8 {
				slug := fmt.Sprintf("h%d", i)
				tunnels[i] = NewTunnel("srv-"+slug, slug, newIdleConn())
				reg.Up(slug, tunnels[i], func() {})
				rev.Check(tunnels[i])
			}
		}()
	}
	wg.Wait()
	t.Cleanup(func() {
		for _, tn := range tunnels {
			tn.Close()
		}
	})
	for _, i := range []int{7, 1500, 2999} {
		st.revoked.Store(tunnels[i].ServerID, true)
	}
	start := time.Now()
	for range 10 {
		for g := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := g; i < homes; i += 8 {
					rev.Changed(tunnels[i].Slug)
				}
			}()
		}
	}
	wg.Wait()
	took := time.Since(start)
	for _, i := range []int{7, 1500, 2999} {
		waitFor(t, tunnels[i].Slug+" to close", func() bool { return tunnels[i].closed.Load() })
	}
	waitFor(t, "the registry", func() bool { return reg.Live() == homes-3 })
	if n := st.atOnce.Load(); n != 1 {
		t.Errorf("%d queries at once, want 1", n)
	}
	if n := st.biggest.Load(); n > standingBatch {
		t.Errorf("a query asked for %d homes, over %d", n, standingBatch)
	}
	// one query takes 20 ms, so the storm's are bounded by its time, never by its 33,000 checks
	if n := st.queries.Load(); n > 200 {
		t.Errorf("%d queries", n)
	}
	// notifying is a set insert: it never waits for the database
	if took > 10*time.Second {
		t.Errorf("30,000 notifications took %v", took)
	}
}

// A slug's tunnels: the live one and the draining one it replaced, and none once they close.
func TestRegistryOfSlug(t *testing.T) {
	reg := NewRegistry()
	a, b := NewTunnel("s", "home", newIdleConn()), NewTunnel("s2", "other", newIdleConn())
	reg.Up("home", a, func() {})
	reg.Up("other", b, func() {})
	if got := reg.OfSlug("home"); len(got) != 1 || got[0] != a {
		t.Fatalf("%v", got)
	}
	if got := reg.OfSlug("none"); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	a.Close()
	waitFor(t, "the closed tunnel to go", func() bool { return len(reg.OfSlug("home")) == 0 })
	if len(reg.OfSlug("other")) != 1 {
		t.Fatal("the other home's tunnel went")
	}
	b.Close()
}

// A full check asked for (the listener came back) and one tunnel of a standby: both roles go.
func TestRevokerChecksEveryRole(t *testing.T) {
	reg := NewRegistry()
	st := &countingStanding{}
	rev := &Revoker{Registry: reg, Store: st, Logger: slog.New(slog.DiscardHandler), Every: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rev.Run(ctx)
	primary, standby := NewTunnel("gone", "p", newIdleConn()), NewTunnel("gone", "q", newIdleConn(), WithPool(true))
	keep := NewTunnel("kept", "k", newIdleConn())
	for _, tn := range []*Tunnel{primary, standby, keep} {
		reg.Up(tn.Slug, tn, func() {})
	}
	st.revoked.Store("gone", true)
	rev.All()
	waitFor(t, "both to close", func() bool { return primary.closed.Load() && standby.closed.Load() })
	if keep.closed.Load() {
		t.Fatal("a home with standing lost its tunnel")
	}
	keep.Close()
}
