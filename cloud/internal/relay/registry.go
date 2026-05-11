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
}

var ErrNoTunnel = errors.New("relay: no live tunnel for slug")

func NewRegistry() *Registry {
	return &Registry{conns: make(map[string]*Tunnel)}
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
// reconnected tunnel.
func (r *Registry) Unregister(slug string, t *Tunnel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.conns[slug]; ok && cur == t {
		delete(r.conns, slug)
	}
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
