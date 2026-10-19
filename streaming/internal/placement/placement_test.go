package placement

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
)

func cand(id, role string, capacity int, eligible bool) Candidate {
	return Candidate{Host: Host{ID: id, Name: id, Role: role, Capacity: capacity, Weight: 1, CanLive: true}, Eligible: eligible}
}

// TC1 of 32.2: home_first.
func TestChooseHomeFirst(t *testing.T) {
	s := DefaultSettings()
	hosts := []Candidate{cand("home", RoleHome, 16, true), cand("pc", RoleNode, 3, true), cand("pc2", RoleNode, 3, true)}
	cases := []struct {
		used map[string]int
		want string
		why  string
	}{
		{map[string]int{"home": 15}, "home", ""},
		{map[string]int{"home": 16, "pc": 1}, "pc2", ""},
		{map[string]int{"home": 16, "pc": 1, "pc2": 2}, "pc", ""},
		{map[string]int{"home": 16, "pc": 3, "pc2": 3}, "", ReasonAllFull},
	}
	for _, c := range cases {
		got := Choose(hosts, c.used, s, Need{}, "", "")
		if got.Host != c.want || got.Why != c.why {
			t.Errorf("used %v: %+v, want %q/%q", c.used, got, c.want, c.why)
		}
	}
	// spill at 75 %: 12 of 16 used → a node
	s.SpillAt = 75
	if got := Choose(hosts, map[string]int{"home": 12}, s, Need{}, "", ""); got.Host != "pc" {
		t.Errorf("spill_at 75: %+v", got)
	}
	// nodes full: back to the home while it has room
	if got := Choose(hosts, map[string]int{"home": 12, "pc": 3, "pc2": 3}, s, Need{}, "", ""); got.Host != "home" {
		t.Errorf("nodes full: %+v", got)
	}
	// only the home online (today)
	if got := Choose(hosts[:1], nil, DefaultSettings(), Need{}, "", ""); got.Host != "home" {
		t.Errorf("home alone: %+v", got)
	}
}

// TC2 of 32.2: balanced ratios and ties (the home wins ties, then weight, then id).
func TestChooseBalanced(t *testing.T) {
	s := DefaultSettings()
	s.Policy = PolicyBalanced
	hosts := []Candidate{cand("home", RoleHome, 16, true), cand("b", RoleNode, 4, true), cand("a", RoleNode, 4, true)}
	if got := Choose(hosts, map[string]int{"home": 8, "a": 1, "b": 1}, s, Need{}, "", ""); got.Host != "a" {
		t.Errorf("lowest ratio, id tie-break: %+v", got)
	}
	if got := Choose(hosts, map[string]int{"home": 4, "a": 1, "b": 1}, s, Need{}, "", ""); got.Host != "home" {
		t.Errorf("home wins ties: %+v", got)
	}
	hosts[1].Weight = 5
	if got := Choose(hosts, map[string]int{"home": 10, "a": 2, "b": 2}, s, Need{}, "", ""); got.Host != "b" {
		t.Errorf("weight breaks ties: %+v", got)
	}
}

// TC3 of 32.2.
func TestPinWinsAndFallsBack(t *testing.T) {
	s := DefaultSettings()
	hosts := []Candidate{cand("home", RoleHome, 16, true), cand("pc", RoleNode, 2, true)}
	if got := Choose(hosts, nil, s, Need{}, "pc", ""); got.Host != "pc" || got.PinSkipped != "" {
		t.Errorf("pin: %+v", got)
	}
	if got := Choose(hosts, map[string]int{"pc": 2}, s, Need{}, "pc", ""); got.Host != "home" || got.PinSkipped != "full" {
		t.Errorf("full pin: %+v", got)
	}
	hosts[1].Eligible, hosts[1].Why = false, "missed_beats"
	if got := Choose(hosts, nil, s, Need{}, "pc", ""); got.Host != "home" || got.PinSkipped != "missed_beats" {
		t.Errorf("down pin: %+v", got)
	}
}

// TC8 of 32.2.
func TestLiveBlocksNeedCanLive(t *testing.T) {
	s := DefaultSettings()
	home := cand("home", RoleHome, 16, true)
	home.CanLive = false
	pc := cand("pc", RoleNode, 2, true)
	if got := Choose([]Candidate{home, pc}, nil, s, Need{Live: true}, "", ""); got.Host != "pc" {
		t.Errorf("live channel: %+v", got)
	}
	pc.CanLive = false
	if got := Choose([]Candidate{home, pc}, nil, s, Need{Live: true}, "", ""); got.Host != "" || got.Why != ReasonNoLiveHost {
		t.Errorf("no live host: %+v", got)
	}
}

// harness: a MemStore, hosts beating, a service.
type harness struct {
	store *MemStore
	hosts *Hosts
	svc   *Service
	mu    sync.Mutex
	now   time.Time
	s     Settings
}

func newHarness(hosts ...Host) *harness {
	h := &harness{store: NewMemStore(), now: time.Unix(1_800_000_000, 0), s: DefaultSettings()}
	h.s.NodesStream = true
	h.store.Now = h.clock
	h.store.SetHosts(hosts)
	h.hosts = &Hosts{Source: h.store, Settings: h, Protocol: 1, Now: h.clock}
	h.svc = &Service{Store: h.store, Hosts: h.hosts, Settings: h, Self: "home"}
	h.hosts.Refresh(context.Background())
	return h
}

func (h *harness) clock() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now }
func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	h.now = h.now.Add(d)
	h.mu.Unlock()
}
func (h *harness) Cluster(context.Context) Settings { h.mu.Lock(); defer h.mu.Unlock(); return h.s }

// setBeat ages a host's last beat.
func (h *harness) setBeat(id string, age time.Duration) {
	hs, _ := h.store.Hosts(context.Background())
	for i := range hs {
		if hs[i].ID == id {
			hs[i].BeatAge = age
		}
	}
	h.store.SetHosts(hs)
}

func homeHost(capacity int) Host {
	return Host{ID: "home", Name: "mac", Role: RoleHome, Enabled: true, Capacity: capacity, Weight: 1, CanLive: true, Protocol: 1}
}

func nodeHost(id string, capacity int) Host {
	return Host{ID: id, Name: id, Role: RoleNode, Enabled: true, Capacity: capacity, Weight: 1, CanLive: true, Protocol: 1}
}

// TC4 of 32.2: concurrent first requests for one channel → one row, one generation.
func TestPlaceConcurrentSameChannel(t *testing.T) {
	h := newHarness(homeHost(16))
	ch := uuid.New()
	var wg sync.WaitGroup
	gens := make(chan int64, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.svc.Invalidate(ch)
			p, err := h.svc.Resolve(context.Background(), ch)
			if err != nil {
				t.Error(err)
				return
			}
			gens <- p.Gen
		}()
	}
	wg.Wait()
	close(gens)
	seen := map[int64]bool{}
	for g := range gens {
		seen[g] = true
	}
	if len(seen) != 1 {
		t.Errorf("generations %v, want one", seen)
	}
}

// TC5 of 32.2: a host with one free slot, two channels at once → one placed there.
func TestPlaceConcurrentCapacity(t *testing.T) {
	h := newHarness(homeHost(1), nodeHost("pc", 1))
	a, b := uuid.New(), uuid.New()
	var wg sync.WaitGroup
	got := make(chan string, 2)
	for _, ch := range []uuid.UUID{a, b} {
		wg.Add(1)
		go func(ch uuid.UUID) {
			defer wg.Done()
			p, err := h.svc.Resolve(context.Background(), ch)
			if err != nil {
				t.Error(err)
				return
			}
			got <- p.Host
		}(ch)
	}
	wg.Wait()
	close(got)
	hostsUsed := map[string]int{}
	for g := range got {
		hostsUsed[g]++
	}
	if hostsUsed["home"] != 1 || hostsUsed["pc"] != 1 {
		t.Errorf("placements %v: each host takes one", hostsUsed)
	}
	// a third channel: no room
	_, err := h.svc.Resolve(context.Background(), uuid.New())
	var nh ErrNoHost
	if !errors.As(err, &nh) || nh.Reason != ReasonAllFull || !errors.Is(err, channel.ErrAtCapacity) {
		t.Errorf("third channel: %v", err)
	}
	if _, last := h.svc.Notices(); last == nil || last.Reason != ReasonAllFull {
		t.Errorf("the refusal is not recorded: %+v", last)
	}
}

// AC8: a placed channel is sticky (never moved to rebalance).
func TestPlacementSticky(t *testing.T) {
	h := newHarness(homeHost(1), nodeHost("pc", 4))
	ch := uuid.New()
	p1, _ := h.svc.Resolve(context.Background(), ch) // home
	h.svc.Invalidate(ch)
	p2, _ := h.svc.Resolve(context.Background(), ch)
	if p1.Host != "home" || p2.Gen != p1.Gen {
		t.Errorf("re-resolve moved the channel: %+v → %+v", p1, p2)
	}
}

// EC2 of 32.2: a placement never acked within 20 s is re-placed.
func TestUnackedPlacementReplaced(t *testing.T) {
	h := newHarness(homeHost(4))
	ch := uuid.New()
	p1, _ := h.svc.Resolve(context.Background(), ch)
	h.advance(AckTimeout + time.Second)
	h.svc.Invalidate(ch)
	p2, _ := h.svc.Resolve(context.Background(), ch)
	if p2.Gen == p1.Gen || p2.Reason != ReasonFailover {
		t.Errorf("unacked placement kept: %+v → %+v", p1, p2)
	}
}

// TC1 of 32.4: 16 s without a beat → down; TC2: three proxy failures → down at once.
func TestDeadAfterMissedBeats(t *testing.T) {
	h := newHarness(homeHost(4), nodeHost("pc", 4))
	if !h.hosts.Alive(context.Background(), "pc") {
		t.Fatal("pc should be online")
	}
	h.setBeat("pc", 16*time.Second)
	down := h.hosts.Refresh(context.Background())
	if len(down) != 1 || down[0] != "pc" || h.hosts.Alive(context.Background(), "pc") {
		t.Errorf("down = %v", down)
	}
	if st, why := h.hosts.State(context.Background(), "pc"); st != StateDown || why != "missed_beats" {
		t.Errorf("state %s %s", st, why)
	}
}

func TestProxyFailuresMarkDown(t *testing.T) {
	h := newHarness(homeHost(4), nodeHost("pc", 4))
	h.hosts.Fail("pc")
	h.hosts.Fail("pc")
	if !h.hosts.Alive(context.Background(), "pc") {
		t.Fatal("two failures must not mark the host down")
	}
	if !h.hosts.Fail("pc") || h.hosts.Alive(context.Background(), "pc") {
		t.Fatal("three failures must mark it down at once")
	}
	// a success in between resets the count
	h2 := newHarness(homeHost(4), nodeHost("pc", 4))
	h2.hosts.Fail("pc")
	h2.hosts.Fail("pc")
	h2.hosts.OK("pc")
	h2.hosts.Fail("pc")
	if !h2.hosts.Alive(context.Background(), "pc") {
		t.Error("failures are counted in a row")
	}
	// the home is never marked down by its own front door
	for i := 0; i < 5; i++ {
		h.hosts.Fail("home")
	}
	if !h.hosts.Alive(context.Background(), "home") {
		t.Error("home marked down")
	}
}

// TC3 of 32.4: only placements with demand are re-placed; the others are released.
func TestFailoverReplacesOnlyDemanded(t *testing.T) {
	h := newHarness(homeHost(4), nodeHost("pc", 4))
	h.s.Policy = PolicyBalanced
	watched, idle := uuid.New(), uuid.New()
	h.store.Put(Placement{Channel: watched, Host: "pc", Gen: 1, State: StateLive, Reason: ReasonFirstRequest, PlacedAt: h.clock()})
	h.store.Put(Placement{Channel: idle, Host: "pc", Gen: 2, State: StateLive, Reason: ReasonFirstRequest, PlacedAt: h.clock()})
	w := &Sweeper{Svc: h.svc, Demand: func(ch uuid.UUID) int {
		if ch == watched {
			return 1
		}
		return 0
	}}
	h.setBeat("pc", time.Minute)
	moved := w.SweepOnce(context.Background())
	if len(moved) != 1 || moved[0] != watched {
		t.Errorf("moved %v", moved)
	}
	p, _, _ := h.store.Get(context.Background(), watched)
	if p.Host != "home" || p.Reason != ReasonFailover || p.Gen <= 2 || !p.Takeover() {
		t.Errorf("watched: %+v", p)
	}
	q, _, _ := h.store.Get(context.Background(), idle)
	if q.State != StateReleased {
		t.Errorf("idle: %+v", q)
	}
	// failover off: released, not moved
	h2 := newHarness(homeHost(4), nodeHost("pc", 4))
	h2.s.Failover = false
	h2.store.Put(Placement{Channel: watched, Host: "pc", Gen: 1, State: StateLive, PlacedAt: h2.clock()})
	w2 := &Sweeper{Svc: h2.svc, Demand: func(uuid.UUID) int { return 3 }}
	h2.setBeat("pc", time.Minute)
	if moved := w2.SweepOnce(context.Background()); len(moved) != 0 {
		t.Errorf("failover off moved %v", moved)
	}
}

// TC5 of 32.4: a host that comes back finds its placement superseded (fencing).
func TestComebackHostStopsSuperseded(t *testing.T) {
	h := newHarness(homeHost(4), nodeHost("pc", 4))
	ch := uuid.New()
	h.store.Put(Placement{Channel: ch, Host: "pc", Gen: 5, State: StateLive, PlacedAt: h.clock()})
	nodeFence := NodeFence{Store: h.store, Self: "pc"}
	if keep, _ := nodeFence.Check(context.Background(), ch, 5); !keep {
		t.Fatal("current placement must keep the station")
	}
	h.setBeat("pc", time.Minute)
	(&Sweeper{Svc: h.svc, Demand: func(uuid.UUID) int { return 1 }}).SweepOnce(context.Background())
	h.setBeat("pc", 0) // back
	h.hosts.Refresh(context.Background())
	if keep, _ := nodeFence.Check(context.Background(), ch, 5); keep {
		t.Error("the comeback host's station must stop")
	}
	// the node's fence refuses a start without the current generation
	ctx := channel.WithPlacementGen(context.Background(), 5)
	if _, err := nodeFence.Allow(ctx, ch); err == nil {
		t.Error("stale generation allowed")
	}
}

// TC6 of 32.4: drain blocks new placements; move now moves every channel.
func TestDrainNoNewPlacements(t *testing.T) {
	pc := nodeHost("pc", 4)
	pc.Draining = true
	h := newHarness(homeHost(0), pc)
	if _, err := h.svc.Resolve(context.Background(), uuid.New()); err == nil {
		t.Error("a draining host took a new channel")
	}
}

func TestMoveNow(t *testing.T) {
	h := newHarness(homeHost(4), nodeHost("pc", 4))
	a, b := uuid.New(), uuid.New()
	h.store.Put(Placement{Channel: a, Host: "pc", Gen: 1, State: StateLive, PlacedAt: h.clock()})
	h.store.Put(Placement{Channel: b, Host: "pc", Gen: 2, State: StateLive, PlacedAt: h.clock()})
	hs, _ := h.store.Hosts(context.Background())
	hs[1].Draining = true
	h.store.SetHosts(hs)
	h.hosts.Reload(context.Background())
	n, err := h.svc.DrainNow(context.Background(), "pc")
	if err != nil || n != 2 {
		t.Fatalf("moved %d %v", n, err)
	}
	for _, ch := range []uuid.UUID{a, b} {
		p, _, _ := h.store.Get(context.Background(), ch)
		if p.Host != "home" || p.Reason != ReasonDrain || p.State != StateMoving {
			t.Errorf("after move now: %+v", p)
		}
	}
	// an explicit move to a named host
	p, err := h.svc.Place(context.Background(), a, ReasonMove, "home")
	if err != nil || p.Host != "home" {
		t.Errorf("move to home: %+v %v", p, err)
	}
	if _, err := h.svc.Place(context.Background(), a, ReasonMove, "pc"); !errors.Is(err, ErrUnknownHost) {
		t.Errorf("move to a draining host: %v", err)
	}
}

// EC3 of 32.4: two down/up cycles in 10 min hold the host out for 10 min.
func TestFlappingHostHeldOut(t *testing.T) {
	h := newHarness(homeHost(4), nodeHost("pc", 4))
	for i := 0; i < 2; i++ {
		h.setBeat("pc", time.Minute)
		h.hosts.Refresh(context.Background())
		h.setBeat("pc", 0)
		h.hosts.Refresh(context.Background())
		h.advance(time.Minute)
	}
	if st, why := h.hosts.State(context.Background(), "pc"); st != StateUnfit || why != "flapping" {
		t.Errorf("state %s %s", st, why)
	}
	h.advance(11 * time.Minute)
	if st, _ := h.hosts.State(context.Background(), "pc"); st != StateOnline {
		t.Errorf("after the hold: %s", st)
	}
}

// nodes_stream off: a node takes nothing (AC9 of 32.1).
func TestNodesStreamOff(t *testing.T) {
	h := newHarness(homeHost(1), nodeHost("pc", 4))
	h.s.NodesStream = false
	_, _ = h.svc.Resolve(context.Background(), uuid.New())
	if _, err := h.svc.Resolve(context.Background(), uuid.New()); err == nil {
		t.Error("a node took a channel with nodes_stream off")
	}
}

func TestParseSettings(t *testing.T) {
	s := ParseSettings([]byte(`{"nodes_stream":true,"policy":"balanced","spill_at":75,"hls_mode":"redirect",
		"failover":false,"dead_after_seconds":20,"pins":{"quran":["pc"],"news":"mac"},"policy_x":1}`))
	if !s.NodesStream || s.Policy != PolicyBalanced || s.SpillAt != 75 || s.HLSMode != HLSRedirect ||
		s.Failover || s.DeadAfter != 20*time.Second || s.Pins["quran"] != "pc" || s.Pins["news"] != "mac" {
		t.Errorf("%+v", s)
	}
	if d := ParseSettings([]byte(`{"policy":"random","spill_at":0}`)); d.Policy != PolicyHomeFirst || d.SpillAt != 100 {
		t.Errorf("bad values keep defaults: %+v", d)
	}
}
