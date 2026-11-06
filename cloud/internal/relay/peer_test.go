package relay

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Story 50.04 T02: good, stale (40 s), wrong key, wrong slug.
func TestPeerAuth(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	a := NewPeerAuth([]byte(strings.Repeat("p", 32)))
	a.now = func() time.Time { return now }
	sig := a.Sign("a-blue", "acme")
	if from, err := a.Verify(sig, "acme"); err != nil || from != "a-blue" {
		t.Fatalf("good: %q %v", from, err)
	}
	if _, err := a.Verify(sig, "other"); !errors.Is(err, ErrPeerAuthBad) {
		t.Errorf("wrong slug: %v", err)
	}
	other := NewPeerAuth([]byte(strings.Repeat("q", 32)))
	other.now = a.now
	if _, err := other.Verify(sig, "acme"); !errors.Is(err, ErrPeerAuthBad) {
		t.Errorf("wrong key: %v", err)
	}
	now = now.Add(40 * time.Second)
	if _, err := a.Verify(sig, "acme"); !errors.Is(err, ErrPeerAuthStale) {
		t.Errorf("stale: %v", err)
	}
	for _, bad := range []string{"", "x", "a.b", "a-blue.nan.sig"} {
		if _, err := a.Verify(bad, "acme"); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// a relay id with dots still parses (the signature is the last part)
	sig = a.Sign("r3.relay", "acme")
	if from, err := a.Verify(sig, "acme"); err != nil || from != "r3.relay" {
		t.Errorf("dotted id: %q %v", from, err)
	}
	var off *PeerAuth
	if off.Sign("a", "b") != "" {
		t.Error("no key signs nothing")
	}
	if _, err := off.Verify(sig, "acme"); err == nil {
		t.Error("no key verifies nothing")
	}
}

// Story 50.04 T01: Up with a newer seq wins; an older tunnel going down on
// another process leaves the newer row.
func TestMemDirectorySeqs(t *testing.T) {
	ctx := context.Background()
	m := NewMemDirectory()
	blue, green := m.For("a-blue", "127.0.0.1:1"), m.For("a-green", "127.0.0.1:2")
	_ = blue.Up(ctx, "acme", "s", 4)
	_ = green.Up(ctx, "acme", "s", 5)
	_ = blue.Up(ctx, "acme", "s", 3) // an older attempt never wins
	_ = blue.Down(ctx, "acme", 4)
	if p, ok := m.Row("acme"); !ok || p.RelayID != "a-green" || p.Seq != 5 {
		t.Fatalf("row %+v %v, want a-green/5", p, ok)
	}
	if p, ok := blue.Lookup(ctx, "acme"); !ok || p.RelayID != "a-green" {
		t.Errorf("blue's lookup: %+v", p)
	}
	if _, ok := green.Lookup(ctx, "acme"); ok {
		t.Error("a process never finds itself")
	}
	m.Drop("a-green")
	if _, ok := blue.Lookup(ctx, "acme"); ok {
		t.Error("a dropped node's rows stay")
	}
}

// A tunnel's seq is the time it was made, strictly increasing: a tunnel made
// later on another process (whose counter would have started earlier) still
// has the higher seq. Found by the rehearsal's drain-1k: green started first,
// so its newer tunnels lost to blue's older ones in the directory.
func TestTunnelSeqIsTheTimeItWasMade(t *testing.T) {
	before := uint64(time.Now().UnixNano())
	a := NewTunnel("s", "acme", &nopConn{done: make(chan struct{})})
	b := NewTunnel("s", "acme", &nopConn{done: make(chan struct{})})
	defer a.Close()
	defer b.Close()
	if a.Seq() < before || b.Seq() <= a.Seq() {
		t.Errorf("seqs %d, %d (made after %d)", a.Seq(), b.Seq(), before)
	}
	// another process's counter that started long ago: its next tunnel is still newer
	tunnelSeq.Store(before - uint64(time.Hour))
	c := NewTunnel("s", "acme", &nopConn{done: make(chan struct{})})
	defer c.Close()
	if c.Seq() <= b.Seq() {
		t.Errorf("a tunnel made later has seq %d, below %d", c.Seq(), b.Seq())
	}
}

type nopConn struct{ done chan struct{} }

func (c *nopConn) ReadFrame() (Frame, error) { <-c.done; return Frame{}, errors.New("closed") }
func (c *nopConn) WriteFrame(Frame) error    { return nil }
func (c *nopConn) Close() error {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	return nil
}
