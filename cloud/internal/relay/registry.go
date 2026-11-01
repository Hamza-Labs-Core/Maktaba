package relay

import (
	"errors"
	"sync"
)

// Registry tracks live server connections. The HTTP proxy looks up a
// tunnel by slug and forwards request frames into it.
//
// Concurrency: read-heavy (every proxied request hits Lookup), with
// writes only on connect/disconnect — we use sync.RWMutex.
type Registry struct {
	mu    sync.RWMutex
	conns map[string]*Tunnel // keyed by server slug

	// slugLocks serialize a slug's tunnel coming and going with the recording of it
	// (Up, Down), apart from mu: the recording is a database write, and Lookup is the
	// hot path
	locksMu   sync.Mutex
	slugLocks map[string]*slugLock
}

type slugLock struct {
	mu    sync.Mutex
	users int
}

var ErrNoTunnel = errors.New("relay: no live tunnel for slug")

func NewRegistry() *Registry {
	return &Registry{conns: make(map[string]*Tunnel), slugLocks: make(map[string]*slugLock)}
}

// lockSlug holds slug's lock until the returned func is called (dropped once no one
// holds or waits for it).
func (r *Registry) lockSlug(slug string) func() {
	r.locksMu.Lock()
	l := r.slugLocks[slug]
	if l == nil {
		l = &slugLock{}
		r.slugLocks[slug] = l
	}
	l.users++
	r.locksMu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		r.locksMu.Lock()
		if l.users--; l.users == 0 {
			delete(r.slugLocks, slug)
		}
		r.locksMu.Unlock()
	}
}

// Up records t's server online (record), then installs t (Register): whatever finds
// the tunnel finds its server online. It runs one at a time with the slug's Down, so a
// reconnect's online is never overwritten by the tunnel it replaced going offline.
//
// An attempt that waited its turn may be stale by then: its connection already gone
// (its reader closed it), or a newer connection for the slug installed meanwhile. Up
// refuses it and records nothing (false): installing it would close the live tunnel
// for a dead one, whose Down would then record the server offline.
func (r *Registry) Up(slug string, t *Tunnel, record func()) bool {
	unlock := r.lockSlug(slug)
	defer unlock()
	if t.closed.Load() {
		return false
	}
	r.mu.RLock()
	cur, ok := r.conns[slug]
	r.mu.RUnlock()
	if ok && cur.seq > t.seq {
		return false
	}
	record()
	r.Register(slug, t)
	return true
}

// Down removes t when it is the slug's live tunnel (Unregister), then records its
// server offline (record), one at a time with the slug's Up. A tunnel a reconnect
// replaced records nothing: that is not the server going offline. It reports whether t
// was the live tunnel.
func (r *Registry) Down(slug string, t *Tunnel, record func()) bool {
	unlock := r.lockSlug(slug)
	defer unlock()
	if !r.Unregister(slug, t) {
		return false
	}
	record()
	return true
}

// Register installs a tunnel. If the slug already has a tunnel we
// close the old one — the new connection wins, on the assumption that
// the server agent reconnected after a network blip.
func (r *Registry) Register(slug string, t *Tunnel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.conns[slug]; ok && old != t {
		old.Close()
	}
	r.conns[slug] = t
}

// Unregister removes a tunnel ONLY if it matches the one we know
// about. This prevents a stale disconnect from evicting a freshly
// reconnected tunnel. It reports whether t was the live tunnel.
func (r *Registry) Unregister(slug string, t *Tunnel) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.conns[slug]; ok && cur == t {
		delete(r.conns, slug)
		return true
	}
	return false
}

// Lookup is the hot path used by the HTTP proxy.
func (r *Registry) Lookup(slug string) (*Tunnel, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.conns[slug]
	if !ok {
		return nil, ErrNoTunnel
	}
	return t, nil
}

// Len returns the number of live tunnels. Used by the metrics collector
// (Epic 30) to sample the connected-server / active-tunnel gauges
// without allocating the full slug slice on the hot sampling path.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.conns)
}

// Counts samples the relay gauges (Epic 30): servers is the number of
// distinct home servers with a live tunnel, tunnels the number of live
// tunnels carrying at least one proxied request right now. A connected
// but idle server counts as connected, not as an active tunnel.
func (r *Registry) Counts() (servers, tunnels int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]struct{}, len(r.conns))
	for slug, t := range r.conns {
		id := t.ServerID
		if id == "" {
			id = "slug:" + slug
		}
		seen[id] = struct{}{}
		if t.Busy() {
			tunnels++
		}
	}
	return len(seen), tunnels
}

// Slugs returns the connected slugs, for diagnostics.
func (r *Registry) Slugs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.conns))
	for s := range r.conns {
		out = append(out, s)
	}
	return out
}
