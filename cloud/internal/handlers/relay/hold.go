package relay

import (
	"net/http"
	"sync"
	"time"

	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Holding a request while its home reconnects (story 50.04 §3).

// Hold defaults.
const (
	DefaultHoldMax    = 15 * time.Second
	DefaultHoldRecent = 2 * time.Minute
	holdLocalEvery    = 250 * time.Millisecond
	holdDirEvery      = time.Second
)

// Holds bounds the requests held at once: per home and per process.
type Holds struct {
	PerHome, Total int

	mu    sync.Mutex
	homes map[string]int
	total int
}

// DefaultHolds: 20 per home, 5,000 per process.
var DefaultHolds = &Holds{PerHome: 20, Total: 5000}

func (h *Holds) acquire(slug string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.homes == nil {
		h.homes = map[string]int{}
	}
	if h.total >= h.Total || h.homes[slug] >= h.PerHome {
		return false
	}
	h.total++
	h.homes[slug]++
	return true
}

func (h *Holds) release(slug string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.total--
	if h.homes[slug]--; h.homes[slug] <= 0 {
		delete(h.homes, slug)
	}
}

// Held is how many requests are held now (tests, diagnostics).
func (h *Holds) Held() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.total
}

func (d *Deps) holds() *Holds {
	if d.Holds != nil {
		return d.Holds
	}
	return DefaultHolds
}

// recentlySeen: the home had a tunnel in the last HoldRecent (here, or as
// the servers table last recorded it).
func (d *Deps) recentlySeen(sv stores.Server) bool {
	if d.HoldMax < 0 {
		return false
	}
	recent := d.HoldRecent
	if recent <= 0 {
		recent = DefaultHoldRecent
	}
	now := d.clock()
	if at, ok := d.Registry.LastSeen(sv.Slug); ok && now.Sub(at) < recent {
		return true
	}
	return sv.LastSeenAt.Valid && now.Sub(sv.LastSeenAt.Time) < recent
}

// hold waits up to HoldMax for slug's tunnel to appear here (checked every
// 250 ms) or on a peer (every 1 s; never for a request a peer forwarded).
// It returns the local tunnel when one came, or served = true when the
// request was forwarded to a peer; neither means the wait ran out (or the
// limits were reached), and the caller answers 503 home_reconnecting.
func (d *Deps) hold(w http.ResponseWriter, r *http.Request, slug string, hopped bool) (t *relaypkg.Tunnel, served bool) {
	hs := d.holds()
	if !hs.acquire(slug) {
		countHeld(HeldRefused)
		return nil, false
	}
	defer hs.release(slug)
	wait := d.HoldMax
	if wait == 0 {
		wait = DefaultHoldMax
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	local := time.NewTicker(holdLocalEvery)
	defer local.Stop()
	dir := time.NewTicker(holdDirEvery)
	defer dir.Stop()
	for {
		select {
		case <-r.Context().Done():
			countHeld(HeldCanceled)
			return nil, false
		case <-deadline.C:
			countHeld(HeldTimeout)
			return nil, false
		case <-local.C:
			if t, err := d.Registry.Lookup(slug); err == nil {
				countHeld(HeldFound)
				return t, false
			}
		case <-dir.C:
			if hopped || d.Directory == nil {
				continue
			}
			if p, ok := d.Directory.Lookup(r.Context(), slug); ok {
				if d.forward(w, r, slug, p) {
					countHeld(HeldFound)
					return nil, true
				}
				d.Directory.Forget(slug)
			}
		}
	}
}
