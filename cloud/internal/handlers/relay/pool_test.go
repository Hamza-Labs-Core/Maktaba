package relay

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
)

// Story 50.16: a pool of relays in one process: each relay its tunnel endpoint, public
// handler, peer listener, heartbeat and rebalancer, sharing a directory and a relays table in
// memory.

type poolRelay struct {
	*proc
	id     string
	pool   *relaypkg.Pool
	stop   context.CancelFunc
	killed atomic.Bool
}

type memPool struct {
	t      *testing.T
	dir    *relaypkg.MemDirectory
	store  *relaypkg.MemPoolStore
	homes  *simhome.Store
	lookup *simLookup
	mu     sync.Mutex
	relays map[string]*poolRelay
}

// get is relay id (nil none).
func (mp *memPool) get(id string) *poolRelay {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	return mp.relays[id]
}

// all is every relay started.
func (mp *memPool) all() []*poolRelay {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	out := make([]*poolRelay, 0, len(mp.relays))
	for _, r := range mp.relays {
		out = append(out, r)
	}
	return out
}

func newMemPool(t *testing.T, ids ...string) *memPool {
	mp := &memPool{t: t, dir: relaypkg.NewMemDirectory(), store: relaypkg.NewMemPoolStore(),
		homes: simhome.NewStore(), lookup: &simLookup{}, relays: map[string]*poolRelay{}}
	for _, id := range ids {
		mp.add(id)
	}
	return mp
}

// add starts relay id and puts it in the pool.
func (mp *memPool) add(id string) *poolRelay {
	t := mp.t
	p := &proc{id: id, reg: relaypkg.NewRegistry(), meter: &fakeMeter{}}
	auth := relaypkg.NewPeerAuth(peerSecret)
	peerHandler := &switchHandler{}
	p.peer = httptest.NewServer(peerHandler)
	view := mp.dir.For(id, strings.TrimPrefix(p.peer.URL, "http://"))
	p.deps = &Deps{Registry: p.reg, Lookup: mp.lookup, Meter: p.meter, PublicHost: "relay.test",
		RelayID: id, Directory: view, PeerAuth: auth, HoldMax: 3 * time.Second, Logger: slog.New(slog.DiscardHandler)}
	peerDeps := *p.deps
	peerDeps.PeerMode = true
	peerHandler.set(peerDeps.Handler())
	p.public = httptest.NewServer(p.deps.Handler())
	pool := relaypkg.NewPool(mp.store, id)
	rb := &relaypkg.Rebalancer{Registry: p.reg, Pool: pool, Hosts: "{id}.relay.test", Every: time.Second, Rate: 200,
		Drain: relaypkg.DrainOptions{Within: 200 * time.Millisecond, Deadline: 20 * time.Second}, Logger: slog.New(slog.DiscardHandler)}
	p.tunnel = httptest.NewServer(&relaypkg.TunnelServer{Registry: p.reg, Store: mp.homes, Drain: true,
		Logger: slog.New(slog.DiscardHandler), Directory: view, Rebalancer: rb})
	ctx, cancel := context.WithCancel(context.Background())
	if err := pool.Start(ctx, relaypkg.PoolRelay{ID: id, PublicIP: "127.0.0.1", PrivateIP: "127.0.0.1", CapacityMbps: 1000}); err != nil {
		t.Fatal(err)
	}
	go rb.Run(ctx)
	r := &poolRelay{proc: p, id: id, pool: pool, stop: cancel}
	mp.mu.Lock()
	mp.relays[id] = r
	mp.mu.Unlock()
	t.Cleanup(func() { r.kill(mp) })
	return r
}

// kill powers relay r off: its listeners and tunnels go at once, its heartbeat stops; its
// directory rows stay until the sweep (what peers see of a dead machine).
func (r *poolRelay) kill(mp *memPool) {
	if !r.killed.CompareAndSwap(false, true) {
		return
	}
	r.stop()
	mp.store.Stop(r.id)
	for _, s := range []*httptest.Server{r.public, r.peer, r.tunnel} {
		s.CloseClientConnections()
	}
	for _, tn := range r.reg.All() {
		tn.Close()
	}
	for _, s := range []*httptest.Server{r.public, r.peer, r.tunnel} {
		s.Close()
	}
}

// resolve maps the relays' tunnel hosts to their endpoints (the homes dial "r2.relay.test").
func (mp *memPool) resolve(host string) (string, bool) {
	id, ok := strings.CutSuffix(host, ".relay.test")
	if !ok {
		return "", false
	}
	r := mp.get(id)
	if r == nil {
		return "", false
	}
	return strings.TrimPrefix(r.tunnel.URL, "http://"), true
}

func (mp *memPool) healthy() []relaypkg.PoolRelay {
	var out []relaypkg.PoolRelay
	for _, r := range mp.all() {
		if !r.killed.Load() {
			out = append(out, relaypkg.PoolRelay{ID: r.id, State: relaypkg.RelayActive, CapacityMbps: 1000})
		}
	}
	return out
}

// placed reports whether home h's tunnels are where its assignment says.
func placed(h *simhome.Home, a relaypkg.Assignment) bool {
	st := h.Sup.State()
	return st.Status == "online" && st.Relay == a.Primary && st.StandbyRelay == a.Standby
}

func waitLong(t *testing.T, d time.Duration, what string, ok func() bool) time.Duration {
	t.Helper()
	start := time.Now()
	for !ok() {
		if time.Since(start) > d {
			t.Fatalf("timed out after %s waiting for %s", d, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return time.Since(start)
}

// startHomes runs n homes, all dialling first (the stable name: any relay), until each holds
// its primary and its standby where its assignment says.
func (mp *memPool) startHomes(api string, first *poolRelay, n int) []*simhome.Home {
	homes := make([]*simhome.Home, n)
	for i := range n {
		homes[i] = simhome.Start(i, first.endpoint(), api, simhome.Options{Resolve: mp.resolve, BaseWait: 100 * time.Millisecond,
			MaxWait: time.Second, PingEvery: time.Second})
		mp.t.Cleanup(homes[i].Stop)
	}
	mp.waitPlaced(homes, 30*time.Second, "every home in its place")
	return homes
}

// waitPlaced waits until every home holds its primary and standby where its assignment says.
func (mp *memPool) waitPlaced(homes []*simhome.Home, d time.Duration, what string) time.Duration {
	start := time.Now()
	for {
		hs := mp.healthy()
		var off []string
		for _, h := range homes {
			if a := relaypkg.Assign(h.ID, hs); !placed(h, a) {
				st := h.Sup.State()
				off = append(off, fmt.Sprintf("%s want %+v has %s/%s/%s", h.Slug, a, st.Status, st.Relay, st.StandbyRelay))
			}
		}
		if len(off) == 0 {
			return time.Since(start)
		}
		if time.Since(start) > d {
			mp.t.Fatalf("timed out after %s waiting for %s: %s", d, what, strings.Join(off, "; "))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// T04: three relays, homes in their places; the relay holding some homes' primaries is
// powered off. Requests for those homes, at every surviving relay, succeed again within 10 s
// (through the standby tunnel, then the promoted primary) and never fail after that; each home
// holds a new standby within 60 s.
func TestPoolRelayLossStandbyTakesOver(t *testing.T) {
	if testing.Short() {
		t.Skip("about 20 s")
	}
	api := simhome.API()
	defer api.Close()
	mp := newMemPool(t, "r1", "r2", "r3")
	homes := mp.startHomes(api.URL, mp.get("r1"), 24)

	victim := "r3"
	var hit []*simhome.Home
	for _, h := range homes {
		if relaypkg.Assign(h.ID, mp.healthy()).Primary == victim {
			hit = append(hit, h)
		}
	}
	if len(hit) == 0 {
		t.Fatal("no home has its primary on r3")
	}
	// every home is reachable through every relay (forwarding)
	for _, h := range homes {
		for _, r := range mp.all() {
			if code := fetch(r.public, h.Slug); code != 200 {
				t.Fatalf("%s via %s: %d", h.Slug, r.id, code)
			}
		}
	}

	type probe struct {
		mu       sync.Mutex
		lastFail time.Time
		fails    int
		ok       int
	}
	probes := map[string]*probe{}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for _, h := range hit {
		for _, rid := range []string{"r1", "r2"} {
			pr := &probe{}
			probes[h.Slug+"@"+rid] = pr
			wg.Add(1)
			go func(srv *httptest.Server, slug string) {
				defer wg.Done()
				for ctx.Err() == nil {
					code := fetch(srv, slug)
					pr.mu.Lock()
					if code == 200 {
						pr.ok++
					} else {
						pr.fails++
						pr.lastFail = time.Now()
					}
					pr.mu.Unlock()
					time.Sleep(50 * time.Millisecond)
				}
			}(mp.get(rid).public, h.Slug)
		}
	}
	time.Sleep(500 * time.Millisecond)
	killed := time.Now()
	mp.get(victim).kill(mp)

	took := mp.waitPlaced(homes, 60*time.Second, "a new standby for every home")
	time.Sleep(2 * time.Second)
	cancel()
	wg.Wait()
	for name, pr := range probes {
		if pr.ok == 0 {
			t.Errorf("%s: no request succeeded", name)
		}
		if !pr.lastFail.IsZero() && pr.lastFail.Sub(killed) > 10*time.Second {
			t.Errorf("%s: a request failed %s after the kill (budget 10 s); %d failed in all", name, pr.lastFail.Sub(killed).Round(time.Millisecond), pr.fails)
		}
	}
	t.Logf("%d homes lost their primary relay; every home had a new standby %s after the kill", len(hit), took.Round(time.Millisecond))
	if took > 60*time.Second {
		t.Errorf("new standbys after %s, budget 60 s", took)
	}
}

// A relay added to the pool: only the homes whose assignment changed move (to it), and no
// request fails while they do.
func TestPoolRelayAddMovesOnlyTheChangedHomes(t *testing.T) {
	if testing.Short() {
		t.Skip("about 15 s")
	}
	api := simhome.API()
	defer api.Close()
	mp := newMemPool(t, "r1", "r2")
	homes := mp.startHomes(api.URL, mp.get("r1"), 24)
	before := map[string]relaypkg.Assignment{}
	for _, h := range homes {
		before[h.ID] = relaypkg.Assign(h.ID, mp.healthy())
	}
	ctx, cancel := context.WithCancel(context.Background())
	var failed atomic.Int64
	var wg sync.WaitGroup
	for _, h := range homes {
		wg.Add(1)
		go func(slug string) {
			defer wg.Done()
			for ctx.Err() == nil {
				if fetch(mp.get("r1").public, slug) != 200 {
					failed.Add(1)
				}
				time.Sleep(100 * time.Millisecond)
			}
		}(h.Slug)
	}
	mp.add("r3")
	mp.waitPlaced(homes, 40*time.Second, "every home in its place after r3 joined")
	cancel()
	wg.Wait()
	movedPrimary := 0
	for _, h := range homes {
		after := relaypkg.Assign(h.ID, mp.healthy())
		if after.Primary != before[h.ID].Primary {
			movedPrimary++
			if after.Primary != "r3" {
				t.Errorf("%s's primary moved %s → %s, not to the new relay", h.Slug, before[h.ID].Primary, after.Primary)
			}
		}
	}
	if movedPrimary == 0 {
		t.Error("no home moved to the new relay")
	}
	if n := failed.Load(); n > 0 {
		t.Errorf("%d requests failed while homes moved", n)
	}
}

func fetch(srv *httptest.Server, slug string) int {
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/health", nil)
	req.Host = slug + ".relay.test"
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 200 && !strings.HasPrefix(string(b), "ok") {
		return -1
	}
	return resp.StatusCode
}

