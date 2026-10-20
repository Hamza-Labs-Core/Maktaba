package nodeauth

// A home outage degrades a node to read-only (rules.cluster.offline_read_minutes): a
// read by a credential the home confirmed is served on that answer for the window,
// marked X-Maktaba-Home: unreachable; a change, an unknown credential and a read past
// the window get 503 home_unreachable.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homelink"
)

// fakeHome answers introspection until it goes down.
type fakeHome struct {
	mu   sync.Mutex
	down bool
}

func (f *fakeHome) setDown(d bool) { f.mu.Lock(); f.down = d; f.mu.Unlock() }
func (f *fakeHome) isDown() bool   { f.mu.Lock(); defer f.mu.Unlock(); return f.down }

func (f *fakeHome) Transport() http.RoundTripper { return http.DefaultTransport }
func (f *fakeHome) HomeURL() string              { return "http://127.0.0.1:1" }
func (f *fakeHome) Introspect(context.Context, delegation.IntrospectRequest) (*delegation.IntrospectAnswer, error) {
	if f.isDown() {
		return nil, homelink.ErrUnreachable
	}
	return &delegation.IntrospectAnswer{Source: "cookie", Principal: delegation.Principal{UserID: "u1"}}, nil
}
func (f *fakeHome) Exchange(context.Context, delegation.ExchangeRequest) (*delegation.ExchangeAnswer, error) {
	return nil, homelink.ErrUnauthorized
}
func (f *fakeHome) JWKS(context.Context) ([]byte, error) { return []byte(`{"keys":[]}`), nil }
func (f *fakeHome) Self(context.Context) (json.RawMessage, error) {
	if f.isDown() {
		return nil, errors.New("down")
	}
	return json.RawMessage(`{}`), nil
}
func (f *fakeHome) Subscribe(func(homelink.Event)) {}
func (f *fakeHome) HasKey() bool                   { return true }

type offlineRig struct {
	home *fakeHome
	r    *Resolver
	h    http.Handler
	now  time.Time
}

func newOfflineRig(window time.Duration) *offlineRig {
	g := &offlineRig{home: &fakeHome{}, now: time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC)}
	g.r = &Resolver{Link: g.home, Now: func() time.Time { return g.now },
		OfflineRead: func() time.Duration { return window }}
	g.h = g.r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if principal.FromContext(req.Context()) == nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	return g
}

func (g *offlineRig) do(method, session string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/videos", nil)
	if session != "" {
		req.AddCookie(&http.Cookie{Name: CookieSession, Value: session})
	}
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}

func TestOfflineReadOnly(t *testing.T) {
	g := newOfflineRig(10 * time.Minute)
	if rec := g.do(http.MethodGet, "s1"); rec.Code != http.StatusOK || rec.Header().Get(HomeStateHeader) != "" {
		t.Fatalf("home up: %d %q", rec.Code, rec.Header().Get(HomeStateHeader))
	}

	g.home.setDown(true)
	g.now = g.now.Add(PositiveTTL + time.Second) // the cached answer has expired
	if rec := g.do(http.MethodGet, "s1"); rec.Code != http.StatusOK || rec.Header().Get(HomeStateHeader) != "unreachable" {
		t.Fatalf("read in an outage: %d %q, want 200 marked unreachable", rec.Code, rec.Header().Get(HomeStateHeader))
	}
	g.now = g.now.Add(DefaultGrace + time.Minute) // past the fail-closed grace, still in the window
	if rec := g.do(http.MethodHead, "s1"); rec.Code != http.StatusOK {
		t.Fatalf("read past the grace: %d, want 200", rec.Code)
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if rec := g.do(m, "s1"); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s in an outage: %d, want 503", m, rec.Code)
		}
	}
	if rec := g.do(http.MethodGet, "never-confirmed"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unknown credential in an outage: %d, want 503", rec.Code)
	}
	if rec := g.do(http.MethodGet, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous read: %d, want the auth gate's 401", rec.Code)
	}

	// a revocation the home pushed before going down still applies
	g.r.Evict(delegation.Revoked{UserID: "u1"})
	if rec := g.do(http.MethodGet, "s1"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("revoked session in an outage: %d, want 503", rec.Code)
	}
}

func TestOfflineReadWindowEnds(t *testing.T) {
	g := newOfflineRig(10 * time.Minute)
	g.do(http.MethodGet, "s1")
	g.home.setDown(true)
	g.now = g.now.Add(PositiveTTL + time.Second)
	if rec := g.do(http.MethodGet, "s1"); rec.Code != http.StatusOK {
		t.Fatalf("read at the outage's start: %d", rec.Code)
	}
	g.now = g.now.Add(10*time.Minute + time.Second)
	if rec := g.do(http.MethodGet, "s1"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("read past the window: %d, want 503", rec.Code)
	}
	// the home answers again (Watch's probe records it): back to normal
	g.home.setDown(false)
	g.r.answered()
	if rec := g.do(http.MethodGet, "s1"); rec.Code != http.StatusOK || rec.Header().Get(HomeStateHeader) != "" {
		t.Fatalf("home back: %d %q", rec.Code, rec.Header().Get(HomeStateHeader))
	}
}

func TestOfflineReadOff(t *testing.T) {
	g := newOfflineRig(0) // offline_read_minutes = 0: the behaviour before
	g.do(http.MethodGet, "s1")
	g.home.setDown(true)
	g.now = g.now.Add(PositiveTTL + time.Second)
	if rec := g.do(http.MethodGet, "s1"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("read with the window off: %d, want 503", rec.Code)
	}
}
