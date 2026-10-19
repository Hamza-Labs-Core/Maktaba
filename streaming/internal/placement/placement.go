package placement

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/tvmetrics"
)

// ErrNoHost is returned when no host can take a channel (R2.5). It matches
// channel.ErrAtCapacity, so the existing answers apply (HLS 503 Retry-After 30, the
// HDHomeRun "all tuners in use").
type ErrNoHost struct{ Reason string }

func (e ErrNoHost) Error() string { return "placement: no host can take the channel: " + e.Reason }

func (e ErrNoHost) Is(target error) bool { return target == channel.ErrAtCapacity }

// ErrUnknownHost: a move names a host that cannot take the channel.
var ErrUnknownHost = errors.New("placement: that host cannot take the channel")

// Cache and timing.
const (
	cacheTTL     = 2 * time.Second
	liveCacheTTL = 5 * time.Minute
	liveHorizon  = 24 * time.Hour
	// AckTimeout: a placement never acked within this is treated as failed (EC2).
	AckTimeout = 20 * time.Second
)

// Service decides where each channel encodes (runs in the home's streaming process).
type Service struct {
	Store    Store
	Hosts    *Hosts
	Settings SettingsSource
	// Self is this (the home's) host id.
	Self   string
	Logger *slog.Logger

	mu        sync.Mutex
	cache     map[uuid.UUID]cached
	live      map[uuid.UUID]liveFact
	slugs     map[uuid.UUID]string
	pinNotes  map[string]string
	lastNoRm  *NoRoom
	listeners []func(uuid.UUID)
}

type cached struct {
	p     Placement
	until time.Time
}

type liveFact struct {
	v  bool
	at time.Time
}

// NoRoom is the last refusal (shown on the admin page).
type NoRoom struct {
	Channel uuid.UUID `json:"channel_id"`
	Reason  string    `json:"reason"`
	At      time.Time `json:"at"`
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) settings(ctx context.Context) Settings {
	if s.Settings == nil {
		return DefaultSettings()
	}
	return s.Settings.Cluster(ctx)
}

func (s *Service) initLocked() {
	if s.cache == nil {
		s.cache = map[uuid.UUID]cached{}
		s.live = map[uuid.UUID]liveFact{}
		s.slugs = map[uuid.UUID]string{}
		s.pinNotes = map[string]string{}
	}
}

// Invalidate drops the cached placement (NOTIFY, a proxy error).
func (s *Service) Invalidate(ch uuid.UUID) {
	s.mu.Lock()
	s.initLocked()
	delete(s.cache, ch)
	s.mu.Unlock()
}

// OnChange registers a callback for placement changes this service makes.
func (s *Service) OnChange(f func(uuid.UUID)) {
	s.mu.Lock()
	s.listeners = append(s.listeners, f)
	s.mu.Unlock()
}

func (s *Service) changed(ch uuid.UUID) {
	s.Invalidate(ch)
	s.mu.Lock()
	ls := append([]func(uuid.UUID){}, s.listeners...)
	s.mu.Unlock()
	for _, f := range ls {
		f(ch)
	}
}

// Cached returns the cached placement without a query (segment fetches).
func (s *Service) Cached(ch uuid.UUID) (Placement, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	c, ok := s.cache[ch]
	if !ok || time.Now().After(c.until) {
		return Placement{}, false
	}
	return c.p, true
}

// need is what the channel needs of its host (live blocks within 24 h, cached).
func (s *Service) need(ctx context.Context, ch uuid.UUID) Need {
	s.mu.Lock()
	s.initLocked()
	f, ok := s.live[ch]
	s.mu.Unlock()
	if ok && time.Since(f.at) < liveCacheTTL {
		return Need{Live: f.v}
	}
	v, err := s.Store.HasLiveBlocks(ctx, ch, liveHorizon)
	if err != nil {
		return Need{Live: ok && f.v}
	}
	s.mu.Lock()
	s.live[ch] = liveFact{v: v, at: time.Now()}
	s.mu.Unlock()
	return Need{Live: v}
}

func (s *Service) slug(ctx context.Context, ch uuid.UUID) string {
	s.mu.Lock()
	s.initLocked()
	v, ok := s.slugs[ch]
	s.mu.Unlock()
	if ok {
		return v
	}
	v, err := s.Store.ChannelSlug(ctx, ch)
	if err != nil {
		return ""
	}
	s.mu.Lock()
	s.slugs[ch] = v
	s.mu.Unlock()
	return v
}

// valid reports whether p still holds its host for need.
func (s *Service) valid(ctx context.Context, p Placement, need Need) bool {
	if !p.Active() || !s.Hosts.Alive(ctx, p.Host) {
		return false
	}
	if p.State != StateLive && p.AckedAt == nil && p.Age > AckTimeout {
		return false // placed but never started (EC2)
	}
	if need.Live {
		if h, ok := s.Hosts.Get(p.Host); ok && !h.CanLive {
			return false
		}
	}
	return true
}

// Resolve returns the channel's placement, placing it when it has none or its host
// is gone (design §3.2).
func (s *Service) Resolve(ctx context.Context, ch uuid.UUID) (Placement, error) {
	if p, ok := s.Cached(ch); ok && s.Hosts.Alive(ctx, p.Host) {
		return p, nil
	}
	need := s.need(ctx, ch)
	p, found, err := s.Store.Get(ctx, ch)
	if err != nil {
		return Placement{}, err
	}
	if found && s.valid(ctx, p, need) {
		s.put(p)
		return p, nil
	}
	reason := ReasonFirstRequest
	if found && p.Active() {
		reason = ReasonFailover
	}
	return s.Place(ctx, ch, reason, "")
}

func (s *Service) put(p Placement) {
	s.mu.Lock()
	s.initLocked()
	s.cache[p.Channel] = cached{p: p, until: time.Now().Add(cacheTTL)}
	s.mu.Unlock()
}

// Place (re)places a channel. For first_request/failover an existing valid placement
// is kept (another request placed it meanwhile); move and drain always take a new
// generation; host names a target ("" lets the balancer choose).
func (s *Service) Place(ctx context.Context, ch uuid.UUID, reason, host string) (Placement, error) {
	need := s.need(ctx, ch)
	st := s.settings(ctx)
	pinName := ""
	if slug := s.slug(ctx, ch); slug != "" {
		pinName = st.Pins[slug]
	}
	var choice Choice
	kept := false
	p, err := s.Store.Place(ctx, ch, func(cur *Placement, used map[string]int) (Decision, error) {
		forced := reason == ReasonMove || reason == ReasonDrain
		if cur != nil && !forced && host == "" && s.valid(ctx, *cur, need) {
			kept = true
			return Decision{Keep: true}, nil
		}
		kept = false
		cands := s.Hosts.Candidates(ctx)
		exclude := ""
		if forced && cur != nil && cur.Active() {
			exclude = cur.Host
		}
		if host != "" {
			for _, c := range cands {
				if c.ID == host || c.Name == host {
					if !c.Eligible || used[c.ID] >= c.Capacity || (need.Live && !c.CanLive) {
						return Decision{}, ErrUnknownHost
					}
					return Decision{Host: c.ID, Reason: reason}, nil
				}
			}
			return Decision{}, ErrUnknownHost
		}
		choice = Choose(cands, used, st, need, pinName, exclude)
		if choice.Host == "" {
			return Decision{}, ErrNoHost{Reason: choice.Why}
		}
		r := reason
		if pinName != "" && choice.PinSkipped == "" && r == ReasonFirstRequest {
			r = ReasonPin
		}
		return Decision{Host: choice.Host, Reason: r}, nil
	})
	s.notePin(pinName, choice.PinSkipped)
	if err != nil {
		var nh ErrNoHost
		if errors.As(err, &nh) {
			s.mu.Lock()
			s.lastNoRm = &NoRoom{Channel: ch, Reason: nh.Reason, At: time.Now()}
			s.mu.Unlock()
			s.log().Warn("tv: no host can take the channel", "channel_id", ch.String(),
				"reason", nh.Reason, "event", "tv_place_no_room")
		}
		return Placement{}, err
	}
	if kept {
		s.put(p)
		return p, nil
	}
	s.log().Info("tv: channel placed", "channel_id", ch.String(), "host_id", p.Host,
		"generation", p.Gen, "reason", p.Reason, "event", "tv_placement")
	tvmetrics.Placements.Inc(p.Reason)
	s.changed(ch)
	s.put(p)
	return p, nil
}

func (s *Service) notePin(pin, why string) {
	if pin == "" {
		return
	}
	s.mu.Lock()
	s.initLocked()
	if why == "" {
		delete(s.pinNotes, pin)
	} else {
		s.pinNotes[pin] = why
	}
	s.mu.Unlock()
}

// Notices are what the admin page shows (pins skipped, the last refusal).
func (s *Service) Notices() (pins map[string]string, last *NoRoom) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	pins = map[string]string{}
	for k, v := range s.pinNotes {
		pins[k] = v
	}
	if s.lastNoRm != nil {
		c := *s.lastNoRm
		last = &c
	}
	return pins, last
}

// Stop stops a channel everywhere (admin).
func (s *Service) Stop(ctx context.Context, ch uuid.UUID) error {
	_, _, err := s.Store.Stop(ctx, ch)
	s.changed(ch)
	return err
}

// DrainNow moves every channel off a host at once ("move now").
func (s *Service) DrainNow(ctx context.Context, host string) (int, error) {
	ps, err := s.Store.OnHost(ctx, host)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range ps {
		if _, err := s.Place(ctx, p.Channel, ReasonDrain, ""); err == nil {
			n++
		}
	}
	return n, nil
}

// HomeFence is the engine's fence on the home: a station starts only for a channel
// resolved to the home (same process as the front door).
type HomeFence struct{ Svc *Service }

func (f HomeFence) Allow(ctx context.Context, ch uuid.UUID) (channel.Grant, error) {
	p, err := f.Svc.Resolve(ctx, ch)
	if err != nil {
		return channel.Grant{}, err
	}
	if p.Host != f.Svc.Self {
		h, _ := f.Svc.Hosts.Get(p.Host)
		return channel.Grant{}, &channel.RunningElsewhereError{HostID: p.Host, Host: h.Name, StreamBaseURL: h.BaseURL}
	}
	return channel.Grant{Gen: p.Gen, Discontinuity: p.Takeover()}, nil
}

func (f HomeFence) Check(ctx context.Context, ch uuid.UUID, gen int64) (bool, int64) {
	return checkRow(ctx, f.Svc.Store, f.Svc.Self, ch, gen)
}

func (f HomeFence) Ack(ctx context.Context, ch uuid.UUID, gen int64) {
	if ok, _ := f.Svc.Store.Ack(ctx, ch, gen); ok {
		f.Svc.Invalidate(ch)
	}
}

func (f HomeFence) Release(ctx context.Context, ch uuid.UUID, gen int64) {
	_, _ = f.Svc.Store.Release(ctx, ch, gen)
	f.Svc.Invalidate(ch)
}

// checkRow: keep while the row names self (adopting a newer generation for self).
func checkRow(ctx context.Context, st Store, self string, ch uuid.UUID, gen int64) (bool, int64) {
	p, found, err := st.Get(ctx, ch)
	if err != nil {
		return true, 0 // cannot read: keep (bounded by the next check)
	}
	if !found || !p.Active() || p.Host != self {
		return false, 0
	}
	if p.Gen != gen {
		return true, p.Gen
	}
	return true, 0
}

// ErrNotPlacedHere: a node got a request for a channel the placement does not give it.
var ErrNotPlacedHere = &channel.RunningElsewhereError{Host: "another host"}

// NodeFence is the engine's fence on a node: the signed front-door request carries
// the generation (X-Maktaba-Placement); the row must name this node with it.
type NodeFence struct {
	Store Store
	Self  string
}

func (f NodeFence) Allow(ctx context.Context, ch uuid.UUID) (channel.Grant, error) {
	gen := channel.PlacementGenFrom(ctx)
	p, found, err := f.Store.Get(ctx, ch)
	if err != nil {
		return channel.Grant{}, err
	}
	if gen == 0 || !found || !p.Active() || p.Host != f.Self || p.Gen != gen {
		return channel.Grant{}, ErrNotPlacedHere
	}
	return channel.Grant{Gen: p.Gen, Discontinuity: p.Takeover()}, nil
}

func (f NodeFence) Check(ctx context.Context, ch uuid.UUID, gen int64) (bool, int64) {
	return checkRow(ctx, f.Store, f.Self, ch, gen)
}

func (f NodeFence) Ack(ctx context.Context, ch uuid.UUID, gen int64) {
	_, _ = f.Store.Ack(ctx, ch, gen)
}

func (f NodeFence) Release(ctx context.Context, ch uuid.UUID, gen int64) {
	_, _ = f.Store.Release(ctx, ch, gen)
}
