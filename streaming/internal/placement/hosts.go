package placement

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Roles.
const (
	RoleHome = "home"
	RoleNode = "node"
)

// Host states (the admin page's words).
const (
	StateOnline   = "online"
	StateDown     = "down"
	StateDraining = "draining"
	StateDisabled = "disabled"
	StateUnfit    = "unfit"
)

// FailuresToDown: consecutive front-door proxy failures that mark a host down at once.
const FailuresToDown = 3

// Flapping: a host that went down twice within flapWindow is held out of new
// placements for flapHold after it comes back (unfit: flapping).
const (
	flapWindow = 10 * time.Minute
	flapHold   = 10 * time.Minute
)

// Host is a streaming host as the balancer sees it (a streaming_hosts row).
type Host struct {
	ID       string
	Name     string
	Role     string
	BaseURL  string
	Enabled  bool
	Draining bool
	// Capacity is the effective capacity: the admin's override, else the reported one.
	Capacity int
	Weight   int
	CanLive  bool
	Unfit    string
	Protocol int
	// BeatAge is how long ago the host last beat (measured by the database clock).
	BeatAge  time.Duration
	Encoders []string
}

// IsHome reports whether the host is the home.
func (h Host) IsHome() bool { return h.Role == RoleHome }

// HostSource lists the streaming hosts.
type HostSource interface {
	Hosts(ctx context.Context) ([]Host, error)
}

// Hosts tracks host health for the balancer (design §2.4): online while it beats
// (within dead_after), its protocol matches, it is not unfit and not marked down by
// front-door failures; it takes new channels only when also enabled, not draining,
// not held out for flapping and — for a node — nodes_stream is on.
type Hosts struct {
	Source   HostSource
	Settings SettingsSource
	// Protocol is this (the home's) cluster protocol version.
	Protocol int
	Now      func() time.Time
	Logger   *slog.Logger

	mu        sync.Mutex
	at        time.Time
	hosts     map[string]Host
	order     []string
	failures  map[string]int
	downMark  map[string]time.Time // marked down by failures at
	alive     map[string]bool      // last computed liveness (transitions)
	downs     map[string][]time.Time
	heldUntil map[string]time.Time
	reasons   map[string]string // why a host is down (for the log and the page)
}

func (h *Hosts) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Hosts) settings(ctx context.Context) Settings {
	if h.Settings == nil {
		return DefaultSettings()
	}
	return h.Settings.Cluster(ctx)
}

func (h *Hosts) init() {
	if h.hosts == nil {
		h.hosts = map[string]Host{}
		h.failures = map[string]int{}
		h.downMark = map[string]time.Time{}
		h.alive = map[string]bool{}
		h.downs = map[string][]time.Time{}
		h.heldUntil = map[string]time.Time{}
		h.reasons = map[string]string{}
	}
}

// Refresh reloads the hosts and returns those that turned down since the last
// refresh (the sweeper re-places their channels).
func (h *Hosts) Refresh(ctx context.Context) []string {
	s, now, ok := h.load(ctx)
	if !ok {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.transitionsLocked(s, now)
}

// Reload re-reads the hosts (an admin change) without consuming the down
// transitions the sweeper acts on.
func (h *Hosts) Reload(ctx context.Context) { h.load(ctx) }

func (h *Hosts) load(ctx context.Context) (Settings, time.Time, bool) {
	list, err := h.Source.Hosts(ctx)
	s := h.settings(ctx)
	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.init()
	if err != nil {
		return s, now, false
	}
	seen := map[string]bool{}
	h.order = h.order[:0]
	for _, x := range list {
		// a beat newer than the failure mark clears it
		if mark, ok := h.downMark[x.ID]; ok && now.Add(-x.BeatAge).After(mark) {
			delete(h.downMark, x.ID)
			h.failures[x.ID] = 0
		}
		h.hosts[x.ID] = x
		seen[x.ID] = true
		h.order = append(h.order, x.ID)
	}
	for id := range h.hosts {
		if !seen[id] {
			delete(h.hosts, id)
		}
	}
	sort.Strings(h.order)
	h.at = now
	return s, now, true
}

func (h *Hosts) transitionsLocked(s Settings, now time.Time) []string {
	var down []string
	for _, id := range h.order {
		a, why := h.aliveLocked(id, s, now)
		was, known := h.alive[id]
		h.alive[id] = a
		if known && was && !a {
			down = append(down, id)
			h.reasons[id] = why
			h.downs[id] = append(pruneOld(h.downs[id], now), now)
			h.log().Warn("tv: streaming host down", "host_id", id, "host", h.hosts[id].Name,
				"reason", why, "event", "tv_host_down")
		}
		if known && !was && a {
			if len(pruneOld(h.downs[id], now)) >= 2 {
				h.heldUntil[id] = now.Add(flapHold)
				h.log().Warn("tv: streaming host flapping; held out of placement", "host_id", id,
					"for", flapHold.String(), "event", "tv_host_flapping")
			}
			h.log().Info("tv: streaming host back", "host_id", id, "event", "tv_host_up")
		}
	}
	return down
}

func pruneOld(ts []time.Time, now time.Time) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < flapWindow {
			out = append(out, t)
		}
	}
	return out
}

func (h *Hosts) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// aliveLocked: may the host keep running its placements?
func (h *Hosts) aliveLocked(id string, s Settings, _ time.Time) (bool, string) {
	x, ok := h.hosts[id]
	if !ok {
		return false, "unknown"
	}
	dead := s.DeadAfter
	if dead <= 0 {
		dead = 15 * time.Second
	}
	if x.BeatAge >= dead {
		return false, "missed_beats"
	}
	if _, marked := h.downMark[id]; marked {
		return false, "proxy_failures"
	}
	if h.Protocol != 0 && x.Protocol != 0 && x.Protocol != h.Protocol {
		return false, "unfit:version"
	}
	if x.Unfit != "" {
		return false, "unfit:" + x.Unfit
	}
	return true, ""
}

// eligibleLocked: may the host take a new placement? (reason when not)
func (h *Hosts) eligibleLocked(id string, s Settings, now time.Time) (bool, string) {
	if a, why := h.aliveLocked(id, s, now); !a {
		return false, why
	}
	x := h.hosts[id]
	switch {
	case !x.Enabled:
		return false, StateDisabled
	case x.Draining:
		return false, StateDraining
	case !x.IsHome() && !s.NodesStream:
		return false, "nodes_stream_off"
	}
	if until, ok := h.heldUntil[id]; ok && now.Before(until) {
		return false, "unfit:flapping"
	}
	return true, ""
}

// Alive reports whether a host may keep its placements.
func (h *Hosts) Alive(ctx context.Context, id string) bool {
	s := h.settings(ctx)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.init()
	a, _ := h.aliveLocked(id, s, h.now())
	return a
}

// Get returns a host.
func (h *Hosts) Get(id string) (Host, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	x, ok := h.hosts[id]
	return x, ok
}

// Candidate is a host with its eligibility for a new placement.
type Candidate struct {
	Host
	Eligible bool
	Why      string
}

// Candidates is every host with whether it may take a new channel.
func (h *Hosts) Candidates(ctx context.Context) []Candidate {
	s := h.settings(ctx)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.init()
	now := h.now()
	out := make([]Candidate, 0, len(h.order))
	for _, id := range h.order {
		ok, why := h.eligibleLocked(id, s, now)
		out = append(out, Candidate{Host: h.hosts[id], Eligible: ok, Why: why})
	}
	return out
}

// Fail counts a failed front-door proxy attempt; the third in a row marks the host
// down at once. Reports whether the host just went down.
func (h *Hosts) Fail(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.init()
	if x, ok := h.hosts[id]; ok && x.IsHome() {
		return false
	}
	h.failures[id]++
	if h.failures[id] >= FailuresToDown {
		if _, marked := h.downMark[id]; !marked {
			h.downMark[id] = h.now()
			h.log().Warn("tv: streaming host down after front-door failures", "host_id", id,
				"failures", h.failures[id], "reason", "proxy_failures", "event", "tv_host_down")
			return true
		}
	}
	return false
}

// OK resets the failure count (a successful proxy attempt).
func (h *Hosts) OK(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.init()
	if h.failures[id] != 0 {
		h.failures[id] = 0
	}
}

// State is a host's state and reason for the admin page.
func (h *Hosts) State(ctx context.Context, id string) (string, string) {
	s := h.settings(ctx)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.init()
	now := h.now()
	if a, why := h.aliveLocked(id, s, now); !a {
		if len(why) > 6 && why[:6] == "unfit:" {
			return StateUnfit, why[6:]
		}
		return StateDown, why
	}
	if ok, why := h.eligibleLocked(id, s, now); !ok {
		switch why {
		case StateDraining:
			return StateDraining, ""
		case StateDisabled, "nodes_stream_off":
			return StateDisabled, why
		}
		return StateUnfit, why[len("unfit:"):]
	}
	return StateOnline, ""
}

// Snapshot lists the hosts (ordered by id).
func (h *Hosts) Snapshot() []Host {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Host, 0, len(h.order))
	for _, id := range h.order {
		out = append(out, h.hosts[id])
	}
	return out
}
