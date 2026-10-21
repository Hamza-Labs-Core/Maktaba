package nodeauth

// A home outage degrades a node to read-only: a credential the home confirmed is used on
// that answer while it holds offline (its own expiry, an answer given no earlier than the
// window before the outage began, an outage younger than the window): reads and this
// machine's own routes (`local`, playback) are served, marked X-Maktaba-Home:
// unreachable; anything else is a 503 home_unreachable, a `local` route answers
// anonymously, and `home` routes are refused at once past the grace. Every request here
// goes through a placement dispatcher, as on a node.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homelink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homeproxy"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/placement"
)

// fakeHome answers introspection until it goes down; its API (behind the proxy) then
// drops every connection.
type fakeHome struct {
	mu      sync.Mutex
	down    bool
	expires time.Time // the answers' expires_at
	proxied int
	srv     *httptest.Server
}

func (f *fakeHome) setDown(d bool) { f.mu.Lock(); f.down = d; f.mu.Unlock() }
func (f *fakeHome) isDown() bool   { f.mu.Lock(); defer f.mu.Unlock(); return f.down }

func (f *fakeHome) Transport() http.RoundTripper { return http.DefaultTransport }
func (f *fakeHome) HomeURL() string              { return f.srv.URL }
func (f *fakeHome) Introspect(_ context.Context, req delegation.IntrospectRequest) (*delegation.IntrospectAnswer, error) {
	if f.isDown() {
		return nil, homelink.ErrUnreachable
	}
	if req.Session == "never-confirmed" || req.PAT == "pat_never" {
		return nil, homelink.ErrUnauthorized
	}
	src := "cookie"
	if req.PAT != "" {
		src = "pat"
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return &delegation.IntrospectAnswer{Source: src, CSRFSHA256: "csrf-hash", ExpiresAt: f.expires,
		Principal: delegation.Principal{UserID: "u1", Username: "amal", Libraries: []string{"lib1"}}}, nil
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

func (f *fakeHome) serveAPI(w http.ResponseWriter, _ *http.Request) {
	if f.isDown() {
		if c, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = c.Close() // the request dies on the wire: the proxy's error path
		}
		return
	}
	f.mu.Lock()
	f.proxied++
	f.mu.Unlock()
	w.WriteHeader(http.StatusTeapot)
}

func (f *fakeHome) proxiedCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.proxied }

type offlineRig struct {
	home  *fakeHome
	r     *Resolver
	h     http.Handler
	now   time.Time
	start time.Time
}

// newOfflineRig builds a node's front over the resolver: a dispatcher with a `local`
// route (the stream sessions, an allowlisted version), an `any` read, `home` sign-in
// routes (proxied); the auth gate answers 401 to an anonymous request.
func newOfflineRig(t *testing.T, window time.Duration) *offlineRig {
	t.Helper()
	g := &offlineRig{home: &fakeHome{}, now: time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC)}
	g.start = g.now
	g.home.expires = g.now.Add(24 * time.Hour)
	g.home.srv = httptest.NewServer(http.HandlerFunc(g.home.serveAPI))
	t.Cleanup(g.home.srv.Close)
	g.r = &Resolver{Link: g.home, Now: func() time.Time { return g.now },
		OfflineRead: func() time.Duration { return window }}

	gate := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p := principal.FromContext(req.Context())
		if p == nil && req.URL.Path != "/api/system/version" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	ok := func(http.ResponseWriter, *http.Request) {}
	routes := chi.NewRouter()
	routes.Get("/api/videos", ok)
	routes.Get("/api/system/version", ok)
	routes.Post("/api/stream/sessions", ok)
	routes.Delete("/api/stream/sessions/{id}", ok)
	routes.Get("/api/auth/me", ok)
	routes.Post("/api/auth/login", ok)
	routes.Post("/api/auth/logout", ok)
	var table placement.Table
	table = append(table, placement.Exact(placement.Local, nil,
		"/api/system/version", "/api/stream/sessions", "/api/stream/sessions/{id}")...)
	table = append(table, placement.Exact(placement.Any, placement.Get, "/api/videos")...)
	table = append(table, placement.Prefixes(placement.Home, "/api/auth/")...)
	g.h = &placement.Dispatcher{Routes: routes, Table: table, Local: g.r.Middleware(gate),
		Proxy: g.r.Home(homeproxy.NewWithFallback(g.home, nil, g.r.Me))}
	return g
}

type answer struct {
	code     int
	marked   bool // X-Maktaba-Home: unreachable
	problem  string
	readable *bool
	body     map[string]any
}

// do sends one request; cred is a session id, "pat_…" for a token, "" for none.
func (g *offlineRig) do(method, path, cred string) answer {
	req := httptest.NewRequest(method, path, nil)
	switch {
	case len(cred) > 4 && cred[:4] == "pat_":
		req.Header.Set("Authorization", "Bearer "+cred)
	case cred != "":
		req.AddCookie(&http.Cookie{Name: CookieSession, Value: cred})
	}
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	a := answer{code: rec.Code, marked: rec.Header().Get(HomeStateHeader) == "unreachable"}
	var body map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &body) == nil {
		a.body = body
		a.problem, _ = body["code"].(string)
		if v, ok := body["readable"].(bool); ok {
			a.readable = &v
		}
	}
	return a
}

// goDown: the home answers (Watch's probe every 5 s) for `after` more, then goes away.
func (g *offlineRig) goDown(after time.Duration) {
	g.now = g.now.Add(after)
	g.r.answered()
	g.home.setDown(true)
}

func (a answer) refusedRead() bool {
	return a.code == http.StatusServiceUnavailable && a.problem == "home_unreachable" && a.readable != nil && !*a.readable
}

func TestOfflineReadsAndPlaybackOnTheLastAnswer(t *testing.T) {
	g := newOfflineRig(t, 10*time.Minute)
	if a := g.do(http.MethodGet, "/api/videos", "s1"); a.code != http.StatusOK || a.marked {
		t.Fatalf("home up: %+v", a)
	}
	g.do(http.MethodGet, "/api/videos", "pat_1")

	g.goDown(PositiveTTL + time.Second) // the cached answers have expired
	for _, c := range []struct{ method, path, cred string }{
		{http.MethodGet, "/api/videos", "s1"},
		{http.MethodHead, "/api/videos", "s1"},
		{http.MethodGet, "/api/videos", "pat_1"},
		// this machine's own routes: playback starts and stops
		{http.MethodPost, "/api/stream/sessions", "s1"},
		{http.MethodDelete, "/api/stream/sessions/abc", "s1"},
		{http.MethodPost, "/api/stream/sessions", "pat_1"},
	} {
		if a := g.do(c.method, c.path, c.cred); a.code != http.StatusOK || !a.marked {
			t.Fatalf("%s %s (%s) in an outage: %d marked=%v, want 200 marked", c.method, c.path, c.cred, a.code, a.marked)
		}
	}
	// the session probe: the home is tried, does not answer, the node answers
	if a := g.do(http.MethodGet, MePath, "s1"); a.code != http.StatusOK || !a.marked {
		t.Fatalf("/api/auth/me in the grace: %+v", a)
	} else if u, _ := a.body["user"].(map[string]any); u["id"] != "u1" || u["username"] != "amal" {
		t.Fatalf("/api/auth/me body: %v", a.body)
	}
	if a := g.do(http.MethodGet, "/api/videos", "never-confirmed"); !a.refusedRead() {
		t.Fatalf("a credential the home never confirmed: %+v, want 503 readable false", a)
	}
	if a := g.do(http.MethodPost, "/api/stream/sessions", "never-confirmed"); a.code != http.StatusUnauthorized || !a.marked {
		t.Fatalf("an unconfirmed credential on a local route: %+v, want anonymous (401)", a)
	}
	if a := g.do(http.MethodGet, "/api/videos", ""); a.code != http.StatusUnauthorized {
		t.Fatalf("anonymous read: %+v, want the auth gate's 401", a)
	}
	if a := g.do(http.MethodGet, "/api/system/version", ""); a.code != http.StatusOK {
		t.Fatalf("an allowlisted local route: %+v", a)
	}

	// past the grace: `home` routes are refused at once, the probe answered here
	g.now = g.now.Add(DefaultGrace + time.Second)
	before := g.home.proxiedCount()
	for _, c := range []struct{ method, path, cred string }{
		{http.MethodPost, "/api/auth/login", ""},
		{http.MethodPost, "/api/auth/logout", "s1"},
	} {
		if a := g.do(c.method, c.path, c.cred); a.code != http.StatusServiceUnavailable || a.problem != "home_unreachable" || a.readable != nil {
			t.Fatalf("%s %s past the grace: %+v, want 503 home_unreachable", c.method, c.path, a)
		}
	}
	if a := g.do(http.MethodGet, MePath, "s1"); a.code != http.StatusOK || !a.marked {
		t.Fatalf("/api/auth/me past the grace: %+v", a)
	}
	if a := g.do(http.MethodGet, MePath, ""); a.code != http.StatusUnauthorized || !a.marked {
		t.Fatalf("/api/auth/me signed out: %+v, want 401 marked", a)
	}
	if a := g.do(http.MethodGet, MePath, "never-confirmed"); !a.refusedRead() {
		t.Fatalf("/api/auth/me unconfirmed: %+v, want 503 readable false", a)
	}
	if g.home.proxiedCount() != before {
		t.Fatalf("the home was tried past the grace")
	}
	if a := g.do(http.MethodPost, "/api/stream/sessions", "s1"); a.code != http.StatusOK {
		t.Fatalf("playback past the grace: %+v", a)
	}

	// the home answers again (Watch's probe records it): back to normal
	g.home.setDown(false)
	g.r.answered()
	if a := g.do(http.MethodGet, "/api/videos", "s1"); a.code != http.StatusOK || a.marked {
		t.Fatalf("home back: %+v", a)
	}
}

func TestOfflineWindowEnds(t *testing.T) {
	g := newOfflineRig(t, 10*time.Minute)
	g.do(http.MethodGet, "/api/videos", "s1")
	g.do(http.MethodGet, "/api/videos", "pat_1")
	g.goDown(PositiveTTL + time.Second)
	if a := g.do(http.MethodGet, "/api/videos", "s1"); a.code != http.StatusOK {
		t.Fatalf("read at the outage's start: %+v", a)
	}
	g.now = g.now.Add(10*time.Minute + time.Second)
	if a := g.do(http.MethodGet, "/api/videos", "s1"); !a.refusedRead() {
		t.Fatalf("read past the window: %+v, want 503 readable false", a)
	}
	if a := g.do(http.MethodGet, MePath, "s1"); !a.refusedRead() {
		t.Fatalf("/api/auth/me past the window: %+v", a)
	}
	// this machine's own routes stay up, anonymously: a cookie or a token no longer counts
	for _, cred := range []string{"s1", "pat_1"} {
		if a := g.do(http.MethodPost, "/api/stream/sessions", cred); a.code != http.StatusUnauthorized {
			t.Fatalf("playback (%s) past the window: %+v, want anonymous (401)", cred, a)
		}
	}
	if a := g.do(http.MethodGet, "/api/system/version", "s1"); a.code != http.StatusOK {
		t.Fatalf("an allowlisted local route past the window: %+v", a)
	}
}

func TestOfflineAnExpiredCredentialIsNotUsed(t *testing.T) {
	g := newOfflineRig(t, 10*time.Minute)
	g.home.expires = g.now.Add(PositiveTTL + 10*time.Second) // the session ends 40 s from now
	g.do(http.MethodGet, "/api/videos", "s1")
	g.do(http.MethodGet, "/api/videos", "pat_1")
	g.goDown(PositiveTTL + time.Second)
	if a := g.do(http.MethodGet, "/api/videos", "s1"); a.code != http.StatusOK {
		t.Fatalf("before the session's expiry: %+v", a)
	}
	g.now = g.home.expires
	if a := g.do(http.MethodGet, "/api/videos", "s1"); !a.refusedRead() {
		t.Fatalf("an expired session in an outage: %+v, want 503 readable false", a)
	}
	if a := g.do(http.MethodGet, MePath, "s1"); !a.refusedRead() {
		t.Fatalf("/api/auth/me of an expired session: %+v", a)
	}
	for _, cred := range []string{"s1", "pat_1"} {
		if a := g.do(http.MethodPost, "/api/stream/sessions", cred); a.code != http.StatusUnauthorized {
			t.Fatalf("an expired %s on a local route: %+v, want anonymous (401)", cred, a)
		}
	}
}

func TestOfflineAStaleAnswerIsNotUsed(t *testing.T) {
	g := newOfflineRig(t, 10*time.Minute)
	g.do(http.MethodGet, "/api/videos", "old")    // confirmed, then not seen for long
	g.now = g.now.Add(2 * time.Minute)            // (a revocation push for it may be lost)
	g.do(http.MethodGet, "/api/videos", "recent") // confirmed 9 min before the outage
	g.goDown(9 * time.Minute)
	if a := g.do(http.MethodGet, "/api/videos", "recent"); a.code != http.StatusOK || !a.marked {
		t.Fatalf("an answer the home gave within the window before the outage: %+v", a)
	}
	if a := g.do(http.MethodGet, "/api/videos", "old"); !a.refusedRead() {
		t.Fatalf("an answer older than the window at the outage's start: %+v, want 503 readable false", a)
	}
	if a := g.do(http.MethodPost, "/api/stream/sessions", "old"); a.code != http.StatusUnauthorized {
		t.Fatalf("a stale answer on a local route: %+v, want anonymous (401)", a)
	}
}

func TestOfflineWindowOffUsesNoLastAnswer(t *testing.T) {
	g := newOfflineRig(t, 0) // offline_read_minutes = 0
	g.do(http.MethodGet, "/api/videos", "s1")
	g.do(http.MethodGet, "/api/videos", "pat_1")
	g.goDown(PositiveTTL + time.Second)
	if a := g.do(http.MethodGet, "/api/videos", "s1"); !a.refusedRead() {
		t.Fatalf("read with the window off: %+v, want 503 readable false", a)
	}
	// the token on a local route is anonymous, as before this setting existed
	for _, cred := range []string{"pat_1", "s1"} {
		if a := g.do(http.MethodPost, "/api/stream/sessions", cred); a.code != http.StatusUnauthorized || !a.marked {
			t.Fatalf("%s on a local route with the window off: %+v, want anonymous (401)", cred, a)
		}
	}
}

func TestOfflineARevocationOrRefusalStillApplies(t *testing.T) {
	g := newOfflineRig(t, 10*time.Minute)
	g.do(http.MethodGet, "/api/videos", "s1")
	g.do(http.MethodGet, "/api/videos", "never-confirmed") // the home said no
	g.r.Evict(delegation.Revoked{UserID: "u1"})            // pushed before the outage
	g.goDown(PositiveTTL + time.Second)
	for _, cred := range []string{"s1", "never-confirmed"} {
		if a := g.do(http.MethodGet, "/api/videos", cred); !a.refusedRead() {
			t.Fatalf("%s in an outage: %+v, want 503 readable false", cred, a)
		}
		if a := g.do(http.MethodPost, "/api/stream/sessions", cred); a.code != http.StatusUnauthorized {
			t.Fatalf("%s on a local route: %+v, want anonymous (401)", cred, a)
		}
	}
}

// A change is never served offline: every write of a node is `home` (proxied, refused at
// once past the grace); one the middleware sees anyway (a write classed `any`) is refused.
func TestOfflineAChangeOutsideLocalRoutesIsRefused(t *testing.T) {
	g := newOfflineRig(t, 10*time.Minute)
	g.do(http.MethodGet, "/api/videos", "s1")
	g.goDown(PositiveTTL + time.Second)
	h := g.r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(m, "/api/videos", nil)
		req.AddCookie(&http.Cookie{Name: CookieSession, Value: "s1"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s outside a local route: %d, want 503", m, rec.Code)
		}
	}
}
