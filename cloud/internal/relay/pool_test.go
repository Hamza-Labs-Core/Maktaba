package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"testing"
	"time"
)

func relaysOf(caps ...int) []PoolRelay {
	out := make([]PoolRelay, len(caps))
	for i, c := range caps {
		out[i] = PoolRelay{ID: fmt.Sprintf("r%d", i+1), State: RelayActive, CapacityMbps: c}
	}
	return out
}

func homeID(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", i) }

// T01: over 100k homes each relay's share of primaries is its share of the capacity within
// ±2 %; every home's standby is another relay; adding one relay to N moves about 1/(N+1) of
// the primaries, every one of them to the new relay.
func TestRendezvousSharesAndMoves(t *testing.T) {
	const homes = 100_000
	for _, caps := range [][]int{{1000, 1000, 1000}, {1000, 2000, 1000, 500}, {1000, 1000, 1000, 1000, 1000, 2000, 1000, 1000, 1000, 1000}} {
		t.Run(fmt.Sprintf("%d relays", len(caps)), func(t *testing.T) {
			rs := relaysOf(caps...)
			total := 0
			for _, c := range caps {
				total += c
			}
			count := map[string]int{}
			before := make([]Assignment, homes)
			for i := range homes {
				a := Assign(homeID(i), rs)
				if a.Primary == "" || a.Standby == "" || a.Primary == a.Standby {
					t.Fatalf("home %d: %+v", i, a)
				}
				before[i] = a
				count[a.Primary]++
			}
			for _, r := range rs {
				want := float64(r.CapacityMbps) / float64(total)
				got := float64(count[r.ID]) / homes
				if math.Abs(got-want) > 0.02*want {
					t.Errorf("%s: %.4f of the primaries, its weight %.4f (beyond ±2 %%)", r.ID, got, want)
				}
			}
			// one more relay of the median capacity
			added := append(append([]PoolRelay(nil), rs...), PoolRelay{ID: "rnew", State: RelayActive, CapacityMbps: 1000})
			moved := 0
			for i := range homes {
				a := Assign(homeID(i), added)
				if a.Primary != before[i].Primary {
					moved++
					if a.Primary != "rnew" {
						t.Fatalf("home %d moved %s → %s, not to the new relay", i, before[i].Primary, a.Primary)
					}
				}
			}
			want := 1000.0 / float64(total+1000)
			if got := float64(moved) / homes; math.Abs(got-want) > 0.1*want {
				t.Errorf("adding a relay moved %.4f of the primaries, want about %.4f", got, want)
			}
		})
	}
}

// Assign is deterministic and ignores the order of the list.
func TestAssignIsDeterministic(t *testing.T) {
	rs := relaysOf(1000, 1000, 1000, 1000)
	rev := []PoolRelay{rs[3], rs[2], rs[1], rs[0]}
	for i := range 1000 {
		if a, b := Assign(homeID(i), rs), Assign(homeID(i), rev); a != b {
			t.Fatalf("home %d: %+v vs %+v", i, a, b)
		}
	}
	if a := Assign(homeID(1), rs[:1]); a.Primary != "r1" || a.Standby != "" {
		t.Fatalf("a pool of one: %+v", a)
	}
	if a := Assign(homeID(1), nil); a != (Assignment{}) {
		t.Fatalf("no relays: %+v", a)
	}
}

// T02: a heartbeat 10 s old is healthy, 20 s old is not; a draining relay is not; the list is
// read at most every 5 s, and a store error keeps the last list.
func TestPoolHealthAndCache(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := NewMemPoolStore()
	store.now = func() time.Time { return now }
	ctx := context.Background()
	for _, id := range []string{"r1", "r2", "r3"} {
		_ = store.Beat(ctx, PoolRelay{ID: id, CapacityMbps: 1000})
	}
	p := NewPool(store, "r1")
	p.now = func() time.Time { return now }
	if got := len(p.Healthy(ctx)); got != 3 {
		t.Fatalf("%d healthy, want 3", got)
	}
	// r2 beat 10 s ago, r3 20 s ago; r1 drained by staff
	now = now.Add(20 * time.Second)
	store.mu.Lock()
	store.beats["r1"] = now
	store.beats["r2"] = now.Add(-10 * time.Second)
	store.mu.Unlock()
	store.SetState("r1", RelayDraining)
	// cached: the old list still answers within 5 s of the last read... (20 s passed: re-read)
	got := p.Healthy(ctx)
	if len(got) != 1 || got[0].ID != "r2" {
		t.Fatalf("healthy %+v, want only r2 (10 s old); r3 is 20 s old, r1 draining", got)
	}
	store.SetState("r1", RelayActive)
	now = now.Add(4 * time.Second)
	if len(p.Healthy(ctx)) != 1 {
		t.Fatal("re-read within 5 s of the last read")
	}
	now = now.Add(2 * time.Second)
	if got := p.Healthy(ctx); len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("after the cache window: %+v, want r1 (r2 now 16 s old)", got)
	}
}

// A relay just back is not settled for a minute: until then a primary on the home's standby
// relay stays (Placed).
func TestPoolSettled(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := NewMemPoolStore()
	store.now = func() time.Time { return now }
	ctx := context.Background()
	_ = store.Beat(ctx, PoolRelay{ID: "r1", CapacityMbps: 1000})
	_ = store.Beat(ctx, PoolRelay{ID: "r2", CapacityMbps: 1000})
	p := NewPool(store, "r2")
	p.now = func() time.Time { return now }
	p.Healthy(ctx)
	b := &Rebalancer{Pool: p}
	a := Assignment{Primary: "r1", Standby: "r2"}
	if b.Placed(false, a) {
		t.Fatal("r1 healthy at the first read is settled: a primary on r2 goes back to it")
	}
	// r1 dies, then comes back
	now = now.Add(PoolHealthyAge + PoolListCache)
	store.mu.Lock()
	store.beats["r2"] = now
	store.mu.Unlock()
	if h := p.Healthy(ctx); len(h) != 1 {
		t.Fatalf("healthy %+v, want r2 only", h)
	}
	now = now.Add(PoolListCache)
	store.mu.Lock()
	store.beats["r1"], store.beats["r2"] = now, now
	store.mu.Unlock()
	p.Healthy(ctx)
	if !b.Placed(false, a) || b.Placed(true, Assignment{Primary: "r2", Standby: "r1"}) {
		t.Fatal("a primary on its standby relay before r1 settled stays; a standby on its primary relay does not")
	}
	now = now.Add(PoolSettleBefore)
	store.mu.Lock()
	store.beats["r1"], store.beats["r2"] = now, now
	store.mu.Unlock()
	p.Healthy(ctx)
	if b.Placed(false, a) {
		t.Fatal("r1 settled: the primary goes back to it")
	}
}

// poolProc is a TunnelServer in a pool of r1 and r2 (memory), as self.
func poolProc(t *testing.T, self string) (*Registry, *httptest.Server, *Rebalancer) {
	t.Helper()
	store := NewMemPoolStore()
	for _, id := range []string{"r1", "r2"} {
		_ = store.Beat(context.Background(), PoolRelay{ID: id, CapacityMbps: 1000})
	}
	reg := NewRegistry()
	rb := &Rebalancer{Registry: reg, Pool: NewPool(store, self), Hosts: "{id}.relay.test",
		Drain: DrainOptions{Within: time.Second, Deadline: 10 * time.Second}}
	srv := httptest.NewServer(&TunnelServer{Registry: reg, Store: &memTunnelStore{}, Drain: true, Rebalancer: rb})
	t.Cleanup(srv.Close)
	return reg, srv, rb
}

// T03: a home on the wrong relay is told its assignment in AUTH_OK and sent
// DRAIN{rebalance} with it; on its primary relay it is not drained.
func TestHandshakeDrainsAMisplacedHome(t *testing.T) {
	want := Assign("srv-1", relaysOf(1000, 1000))
	wrong := "r1"
	if want.Primary == "r1" {
		wrong = "r2"
	}
	_, srv, _ := poolProc(t, wrong)
	c, f := dialAuth(t, srv.URL, AuthPayload{ServerID: "srv-1", Secret: "sek", Features: []string{FeatureDrain, FeaturePool}})
	defer func() { _ = c.Close() }()
	var ok AuthOKPayload
	if f.Kind != KindAuthOK || json.Unmarshal(f.Payload, &ok) != nil || ok.Relays == nil {
		t.Fatalf("AUTH_OK: kind %#x %s", f.Kind, f.Payload)
	}
	if ok.Relays.Primary.ID != want.Primary || ok.Relays.Primary.Host != want.Primary+".relay.test" || ok.Relays.Here != wrong ||
		ok.Relays.Standby == nil || ok.Relays.Standby.ID != want.Standby {
		t.Fatalf("AUTH_OK relays %+v, want %+v here %s", ok.Relays, want, wrong)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	d, _ := ReadFrame(&byteReader{b: data})
	var dp DrainPayload
	if d.Kind != KindDrain || json.Unmarshal(d.Payload, &dp) != nil || dp.Reason != DrainRebalance || dp.Relays == nil || dp.Relays.Primary.ID != want.Primary {
		t.Fatalf("after AUTH_OK: kind %#x %s, want DRAIN{rebalance} to %s", d.Kind, d.Payload, want.Primary)
	}

	// on its primary relay: AUTH_OK, then nothing
	_, srv2, _ := poolProc(t, want.Primary)
	c2, f2 := dialAuth(t, srv2.URL, AuthPayload{ServerID: "srv-1", Secret: "sek", Features: []string{FeatureDrain, FeaturePool}})
	defer func() { _ = c2.Close() }()
	if f2.Kind != KindAuthOK {
		t.Fatalf("AUTH_OK: %#x", f2.Kind)
	}
	_ = c2.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, data, err := c2.ReadMessage(); err == nil {
		t.Fatalf("a placed home was sent %x", data)
	}
}

// A home without the pool feature is never told an assignment nor moved; a standby tunnel to a
// relay outside a pool is refused.
func TestHandshakeOlderHomeAndNoPool(t *testing.T) {
	want := Assign("srv-1", relaysOf(1000, 1000))
	wrong := "r1"
	if want.Primary == "r1" {
		wrong = "r2"
	}
	_, srv, _ := poolProc(t, wrong)
	c, f := dialAuth(t, srv.URL, AuthPayload{ServerID: "srv-1", Secret: "sek", Features: []string{FeatureDrain}})
	defer func() { _ = c.Close() }()
	var ok AuthOKPayload
	if f.Kind != KindAuthOK || json.Unmarshal(f.Payload, &ok) != nil || ok.Relays != nil {
		t.Fatalf("AUTH_OK to an older home: %s", f.Payload)
	}
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, data, err := c.ReadMessage(); err == nil {
		t.Fatalf("an older home was sent %x", data)
	}

	reg := NewRegistry()
	plain := httptest.NewServer(&TunnelServer{Registry: reg, Store: &memTunnelStore{}})
	defer plain.Close()
	c2, f2 := dialAuth(t, plain.URL, AuthPayload{ServerID: "srv-1", Secret: "sek", Features: []string{FeaturePool}, Role: RoleStandby})
	defer func() { _ = c2.Close() }()
	if f2.Kind != KindAuthFail {
		t.Fatalf("a standby without a pool: %#x %s", f2.Kind, f2.Payload)
	}
}

// A standby never displaces the home's primary on the same relay; a primary retires a standby
// (it closes once idle) rather than cutting it.
func TestRegistryStandbyAndPrimary(t *testing.T) {
	reg := NewRegistry()
	mk := func(standby bool) *Tunnel {
		return NewTunnel("srv-1", "acme", &nopConn{done: make(chan struct{})}, WithPool(standby))
	}
	primary := mk(false)
	if !reg.Up("acme", primary, func() {}) {
		t.Fatal("primary refused")
	}
	sb := mk(true)
	if reg.Up("acme", sb, func() {}) {
		t.Fatal("a standby displaced the live primary")
	}
	reg2 := NewRegistry()
	sb2 := mk(true)
	if !reg2.Up("acme", sb2, func() {}) {
		t.Fatal("a standby refused on a relay without the primary")
	}
	p2 := mk(false)
	if !reg2.Up("acme", p2, func() {}) {
		t.Fatal("the promoted primary refused")
	}
	if cur, _ := reg2.Lookup("acme"); cur != p2 {
		t.Fatal("the primary is not the slug's tunnel")
	}
	if !sb2.Draining() {
		t.Fatal("the standby was not retired")
	}
	waitFor(t, "the idle standby closed", func() bool {
		select {
		case <-sb2.Done():
			return true
		default:
			return false
		}
	})
}
