// Package timelimit is what the streaming service keeps of a restricted account's
// time limit (Epic 57 story 57.14). The API is the time keeper: it pushes, for an
// account in a library, the moment its playback ends, and for each stream session
// of a restricted account a short lease that only a counted call to the API
// renews. Streaming serves such a session only before the account's end and while
// that session's own lease runs. A stream URL of a restricted account names its
// session (the tls claim): with no lease for it here (never given, ended, or lost
// with a restart) nothing is served until the API gives one. A URL that names no
// session is served only to an account nothing is held for here: the API signs
// every URL of an account whose time it keeps with the claim, so one without it,
// for an account that has an entry, is refused. The API also says when an account
// becomes kept (SetKept), so a URL signed before that is refused in every library,
// and when it stops being kept, so nothing stale refuses it afterwards.
package timelimit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
)

// Reasons of a refusal, as the API's parental-time problem names them.
const (
	ReasonOutsideSlot  = "outside_slot"
	ReasonLimitReached = "limit_reached"
	ReasonNoHeartbeat  = "no_heartbeat"

	// Problem is the problem type of a refusal.
	Problem = "parental-time"

	leasesKept = 8192
	leaseKeep  = 24 * time.Hour // an ended lease is kept this long, for what it served
)

// ErrEnded stops a response under way when the account's time ended or the
// session's lease ran out.
var ErrEnded = errors.New("timelimit: playback time ended")

// Limit is what the API pushes for an account in a library.
type Limit struct {
	EndAt         *time.Time // playback ends here; nil: no end known
	Reason        string
	NextAllowedAt *time.Time
	SessionID     string     // the stream session LeaseUntil is for
	LeaseUntil    *time.Time // nil: leases stay as they are
}

// Refusal says why a request is not served.
type Refusal struct {
	Reason        string
	NextAllowedAt *time.Time
}

type end struct {
	at     *time.Time
	reason string
	next   *time.Time
}

// lease is one stream session's. free: the account's time stopped being kept
// while the session ran (SetKept false), so it plays on with no counted call until
// the account is kept again.
type lease struct {
	until, served time.Time
	free          bool
}

// Registry holds the ends and leases. The zero value is not usable: New.
type Registry struct {
	Now func() time.Time
	// LeasesKept bounds the leases (0: leasesKept). When it is reached the lease
	// that ended longest ago goes: its session is served nothing until the API
	// gives it a new one.
	LeasesKept int
	// Sleep waits d or until ctx ends (nil: a timer). Pacing waits through it, so a
	// test paces on its own clock.
	Sleep func(ctx context.Context, d time.Duration) error

	mu     sync.Mutex
	ends   map[string]end     // user + "|" + library
	leases map[string]*lease  // user + "|" + session
	kept   map[string]bool    // accounts the API said are kept, whatever library
	pace   map[string]*bucket // user + "|" + video (pace.go)
}

// New is an empty registry.
func New() *Registry {
	return &Registry{ends: map[string]end{}, leases: map[string]*lease{}, kept: map[string]bool{}}
}

func (g *Registry) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// Set keeps what the API pushed for userID in libraryID. A nil limit removes the
// account's end there; leases only ever change through a limit that names one.
func (g *Registry) Set(userID, libraryID string, lim *Limit) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if lim == nil {
		delete(g.ends, userID+"|"+libraryID)
		return
	}
	g.ends[userID+"|"+libraryID] = end{lim.EndAt, lim.Reason, lim.NextAllowedAt}
	if lim.SessionID == "" || lim.LeaseUntil == nil {
		return
	}
	key := userID + "|" + lim.SessionID
	l := g.leases[key]
	if l == nil {
		g.room()
		l = &lease{}
		g.leases[key] = l
	}
	l.until, l.free = *lim.LeaseUntil, false
}

// SetKept is the API's word that userID's playback time is kept from now on, or no
// longer is.
//
// Kept (the account was made restricted): a URL signed before that, which names no
// session (tk "0"), is refused from here on in every library, and a session left
// free by an earlier lift is served nothing until a counted call renews its lease.
//
// Not kept (the restriction was lifted): everything held for the account goes, in
// every library, so its new URLs (tk "0") are served; a session that was running
// under a lease plays on, free, with no further call.
func (g *Registry) SetKept(userID string, kept bool) {
	now := g.now()
	prefix := userID + "|"
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.kept == nil {
		g.kept = map[string]bool{}
	}
	if kept {
		g.kept[userID] = true
		for k, l := range g.leases {
			if l.free && strings.HasPrefix(k, prefix) {
				l.free = false
				if l.until.After(now) {
					l.until = now
				}
			}
		}
		return
	}
	delete(g.kept, userID)
	g.clearEnds(prefix)
	for k, l := range g.leases {
		if strings.HasPrefix(k, prefix) {
			l.until, l.free = now.Add(leaseKeep), true
		}
	}
}

// ClearEnds drops the end held for userID in every library (the account has no
// time rule left). Its leases stay: it is still kept, and still counted.
func (g *Registry) ClearEnds(userID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.clearEnds(userID + "|")
}

// clearEnds is called with the lock held.
func (g *Registry) clearEnds(prefix string) {
	for k := range g.ends {
		if strings.HasPrefix(k, prefix) {
			delete(g.ends, k)
		}
	}
}

// room makes place for one more lease. Called with the lock held.
func (g *Registry) room() {
	limit := g.LeasesKept
	if limit <= 0 {
		limit = leasesKept
	}
	if len(g.leases) < limit {
		return
	}
	old := g.now().Add(-leaseKeep)
	for k, l := range g.leases {
		if l.until.Before(old) {
			delete(g.leases, k)
		}
	}
	for len(g.leases) >= limit {
		oldest := ""
		for k, l := range g.leases {
			if oldest == "" || l.until.Before(g.leases[oldest].until) {
				oldest = k
			}
		}
		delete(g.leases, oldest)
	}
}

// Check says whether userID may be served now from libraries, for the stream
// session its URL names. sessionID "": the URL names none; it is served only when
// nothing is held for the account in those libraries, and refused when anything is
// (the account's time is kept, and this URL has no lease to be counted under).
// deadline is when to ask again (zero: never).
func (g *Registry) Check(userID string, libraries []string, sessionID string) (deadline time.Time, refused *Refusal) {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	kept := g.kept[userID]
	for _, lib := range libraries {
		e, ok := g.ends[userID+"|"+lib]
		kept = kept || ok
		if !ok || e.at == nil {
			continue
		}
		if !now.Before(*e.at) {
			reason := e.reason
			if reason == "" {
				reason = ReasonLimitReached
			}
			return time.Time{}, &Refusal{Reason: reason, NextAllowedAt: e.next}
		}
		if deadline.IsZero() || e.at.Before(deadline) {
			deadline = *e.at
		}
	}
	if sessionID == "" {
		if kept {
			return time.Time{}, &Refusal{Reason: ReasonNoHeartbeat}
		}
		return time.Time{}, nil
	}
	l := g.leases[userID+"|"+sessionID]
	if l == nil || !now.Before(l.until) {
		return time.Time{}, &Refusal{Reason: ReasonNoHeartbeat}
	}
	if deadline.IsZero() || l.until.Before(deadline) {
		deadline = l.until
	}
	if now.After(l.served) {
		l.served = now
	}
	return deadline, nil
}

// Served is when the session was last served anything; known is false when there
// is no lease for it here.
func (g *Registry) Served(userID, sessionID string) (at time.Time, known bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	l := g.leases[userID+"|"+sessionID]
	if l == nil {
		return time.Time{}, false
	}
	return l.served, true
}

// NotKept is the tk claim of a stream URL signed for an account whose playback
// time is not kept; Kept is the claim of one whose time is.
const (
	NotKept = "0"
	Kept    = "1"
)

// Guard refuses a request the registry does not allow with a 403 parental-time
// problem, and stops a response under way once the end or the lease is past. It
// runs after auth.SignedURL. A URL is served without a lease only when the API
// signed it as one of an account whose time is not kept (tk "0") and nothing is
// held here for the account; missing state never means unlimited, so a URL with
// neither claim is refused whatever the registry holds, also when it is empty
// after a restart. A nil registry is an empty one: no URL that names a session is
// served.
func Guard(g *Registry) func(http.Handler) http.Handler {
	if g == nil {
		g = New()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok || claims == nil {
				next.ServeHTTP(w, r)
				return
			}
			if claims.TLS == "" && claims.Tk != NotKept {
				refuse(w, &Refusal{Reason: ReasonNoHeartbeat})
				return
			}
			deadline, refused := g.Check(claims.Usr, claims.Lib, claims.TLS)
			if refused != nil {
				refuse(w, refused)
				return
			}
			if claims.TLS != "" {
				// an account whose time is kept: its media is paced to playback rate
				// (pace.go), so the seconds its lease runs are the seconds it watches
				r = r.WithContext(context.WithValue(r.Context(), pacerKey{}, &Pacer{g: g, user: claims.Usr}))
			}
			if deadline.IsZero() {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(&writer{ResponseWriter: w, g: g, claims: claims, deadline: deadline, asked: g.now()}, r)
		})
	}
}

func refuse(w http.ResponseWriter, ref *Refusal) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(httpx.ProblemHeader, Problem)
	w.WriteHeader(http.StatusForbidden)
	body := map[string]any{
		"type":   "https://maktaba.invalid/problems/" + Problem,
		"title":  "Playback time is over",
		"status": http.StatusForbidden,
		"reason": ref.Reason,
	}
	if ref.NextAllowedAt != nil {
		body["next_allowed_at"] = ref.NextAllowedAt.UTC().Format(time.RFC3339)
	}
	_ = json.NewEncoder(w).Encode(body)
}

// writer asks the registry again while a long response (a direct-play file) is
// written: every second, so the session's served time is kept, and at the deadline.
type writer struct {
	http.ResponseWriter
	g        *Registry
	claims   *auth.Claims
	deadline time.Time
	asked    time.Time
	ended    bool
	// typed: the headers were prepared for a body that is passed on
	typed bool
}

// WriteHeader sends the status with nosniff, and the body's type never a page's
// (httpx.PassHeaders).
func (w *writer) WriteHeader(status int) {
	rw := w.ResponseWriter
	if !w.typed {
		w.typed = true
		if ct := httpx.PassHeaders(rw.Header(), status); ct != "" {
			rw.Header().Set("Content-Type", ct)
		}
	}
	rw.WriteHeader(status)
}

func (w *writer) Write(p []byte) (int, error) {
	if w.ended {
		return 0, ErrEnded
	}
	if now := w.g.now(); !now.Before(w.deadline) || now.Sub(w.asked) >= time.Second {
		deadline, refused := w.g.Check(w.claims.Usr, w.claims.Lib, w.claims.TLS)
		if refused != nil {
			w.ended = true
			return 0, ErrEnded
		}
		if deadline.IsZero() { // the limit was lifted
			deadline = now.Add(time.Hour)
		}
		w.deadline, w.asked = deadline, now
	}
	rw := w.ResponseWriter
	if !w.typed {
		w.typed = true
		if ct := httpx.PassHeaders(rw.Header(), http.StatusOK); ct != "" {
			rw.Header().Set("Content-Type", ct)
		}
	}
	return rw.Write(p)
}

// Flush and Unwrap keep the wrapped writer's streaming behaviour.
func (w *writer) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }
