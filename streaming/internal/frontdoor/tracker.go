package frontdoor

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// hlsViewerWindow: an HLS client that fetched within this counts as watching.
const hlsViewerWindow = 30 * time.Second

// hlsForget: an HLS client silent this long is forgotten.
const hlsForget = 10 * time.Minute

// Tracker counts the front door's viewers per channel: TS connections (tuners,
// live.ts) and HLS clients by address. It is the failover sweeper's demand and the
// admin page's viewer counts.
type Tracker struct {
	// WarmGrace: an HLS touch within this keeps the channel in demand (nil: 60 s).
	WarmGrace func() time.Duration
	Now       func() time.Time

	mu   sync.Mutex
	ts   map[uuid.UUID]int
	push map[uuid.UUID]int
	hls  map[uuid.UUID]map[string]time.Time
}

// NewTracker builds an empty tracker.
func NewTracker() *Tracker {
	return &Tracker{ts: map[uuid.UUID]int{}, push: map[uuid.UUID]int{}, hls: map[uuid.UUID]map[string]time.Time{}}
}

// AttachPush records a push relay feeding on the channel (demand, but not a
// human viewer: the hot standby's warm set ranks it after them, story 33.1).
func (t *Tracker) AttachPush(ch uuid.UUID) func() {
	t.mu.Lock()
	if t.push == nil {
		t.push = map[uuid.UUID]int{}
	}
	t.push[ch]++
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			if t.push[ch] > 1 {
				t.push[ch]--
			} else {
				delete(t.push, ch)
			}
			t.mu.Unlock()
		})
	}
}

// ChannelDemand is one channel's demand: human viewers (tuners and HLS clients
// within the warm grace) and push relays.
type ChannelDemand struct {
	Channel uuid.UUID
	Humans  int
	Pushes  int
}

// Demands lists every channel with demand.
func (t *Tracker) Demands() []ChannelDemand {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := map[uuid.UUID]*ChannelDemand{}
	get := func(ch uuid.UUID) *ChannelDemand {
		d := m[ch]
		if d == nil {
			d = &ChannelDemand{Channel: ch}
			m[ch] = d
		}
		return d
	}
	for ch, n := range t.ts {
		get(ch).Humans += n
	}
	for ch, n := range t.push {
		get(ch).Pushes += n
	}
	cut := t.now().Add(-t.grace())
	for ch, hm := range t.hls {
		for _, at := range hm {
			if at.After(cut) {
				get(ch).Humans++
			}
		}
	}
	out := make([]ChannelDemand, 0, len(m))
	for _, d := range m {
		out = append(out, *d)
	}
	return out
}

func (t *Tracker) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// AttachTS records a TS client; the returned func detaches it.
func (t *Tracker) AttachTS(ch uuid.UUID) func() {
	t.mu.Lock()
	t.ts[ch]++
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			if t.ts[ch] > 1 {
				t.ts[ch]--
			} else {
				delete(t.ts, ch)
			}
			t.mu.Unlock()
		})
	}
}

// TSClients is the TS clients per channel (tuners and live.ts; not the push
// relays): with the app viewings, channel_runtime.viewer_count.
func (t *Tracker) TSClients() map[uuid.UUID]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[uuid.UUID]int, len(t.ts))
	for ch, n := range t.ts {
		out[ch] = n
	}
	return out
}

// TouchHLS records an HLS fetch by the request's client.
func (t *Tracker) TouchHLS(ch uuid.UUID, r *http.Request) {
	key := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		key = host
	}
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		key = f // the web port's gate forwards for the browser
	}
	t.mu.Lock()
	m := t.hls[ch]
	if m == nil {
		m = map[string]time.Time{}
		t.hls[ch] = m
	}
	m[key] = t.now()
	t.mu.Unlock()
}

func (t *Tracker) grace() time.Duration {
	if t.WarmGrace != nil {
		if g := t.WarmGrace(); g > 0 {
			return g
		}
	}
	return 60 * time.Second
}

// Demand is the channel's viewer count for the sweeper (TS clients + HLS clients
// within the warm grace).
func (t *Tracker) Demand(ch uuid.UUID) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := t.ts[ch] + t.push[ch]
	cut := t.now().Add(-t.grace())
	for k, at := range t.hls[ch] {
		if at.After(cut) {
			n++
		} else if t.now().Sub(at) > hlsForget {
			delete(t.hls[ch], k)
		}
	}
	return n
}

// Viewers is a channel's current viewers.
type Viewers struct {
	HLS    int `json:"hls"`
	Tuners int `json:"tuners"`
}

// Snapshot is every channel's current viewers. It also forgets HLS clients
// silent for hlsForget (and channels left with none): the routes are open, so
// fetches for any channel id from any address would otherwise pile up for good
// (Demand prunes only the channels the sweeper asks about).
func (t *Tracker) Snapshot() map[string]Viewers {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[string]Viewers{}
	now := t.now()
	cut := now.Add(-hlsViewerWindow)
	for ch, m := range t.hls {
		for k, at := range m {
			if now.Sub(at) > hlsForget {
				delete(m, k)
			}
		}
		if len(m) == 0 {
			delete(t.hls, ch)
		}
	}
	for ch, n := range t.ts {
		v := out[ch.String()]
		v.Tuners = n
		out[ch.String()] = v
	}
	for ch, m := range t.hls {
		c := 0
		for _, at := range m {
			if at.After(cut) {
				c++
			}
		}
		if c > 0 {
			v := out[ch.String()]
			v.HLS = c
			out[ch.String()] = v
		}
	}
	return out
}
