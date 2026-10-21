package nodeauth

// A home outage degrades a node to read-only, on this machine's copy of the home's
// database: a credential the copy says is live is served for reads and this machine's
// own routes (`local`: playback, TV), marked X-Maktaba-Home: unreachable; a change
// through the home's database is a 503 home_unreachable; a sign-in is checked against
// the copy and makes a node-local session. With no copy, or one that was behind when
// the home was last heard, a `local` route answers anonymously and anything else is a
// 503. Every request here goes through a placement dispatcher, as on a node.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func init() { signInFloor = 0 }

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
	if req.Session == "signed-out" || req.PAT == "pat_revoked" {
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

// fakeCopy is this machine's copy: the home's sessions and tokens as of its last beat,
// two users, one of them waiting on a password change.
type fakeCopy struct {
	mu      sync.Mutex
	synced  time.Time
	broken  bool
	expires time.Time
}

func (c *fakeCopy) set(f func(*fakeCopy)) { c.mu.Lock(); f(c); c.mu.Unlock() }

var copyUsers = map[string]delegation.Principal{
	"u1": {UserID: "u1", Username: "amal", Libraries: []string{"lib1"}},
	"u2": {UserID: "u2", Username: "badr", MustChangePassword: true},
}

func (c *fakeCopy) Introspect(_ context.Context, req delegation.IntrospectRequest) (*delegation.IntrospectAnswer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken {
		return nil, errors.New("copy: connection refused")
	}
	switch {
	case req.Session == "s1":
		return &delegation.IntrospectAnswer{Principal: copyUsers["u1"], Source: "cookie", CSRFSHA256: "csrf-hash",
			ExpiresAt: c.expires}, nil
	case req.PAT == "pat_1":
		return &delegation.IntrospectAnswer{Principal: copyUsers["u1"], Source: "pat", ExpiresAt: c.expires}, nil
	}
	return nil, ErrNotLive
}

func (c *fakeCopy) Principal(_ context.Context, userID string) (*delegation.Principal, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken {
		return nil, errors.New("copy: connection refused")
	}
	p, ok := copyUsers[userID]
	if !ok {
		return nil, ErrNotLive
	}
	return &p, nil
}

func (c *fakeCopy) SignIn(_ context.Context, username, password string) (*delegation.Principal, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken {
		return nil, errors.New("copy: connection refused")
	}
	for _, p := range copyUsers {
		if strings.EqualFold(p.Username, username) && password == "pw-"+p.Username {
			return &p, nil
		}
	}
	return nil, ErrNotLive
}

func (c *fakeCopy) Synced(context.Context) (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken {
		return time.Time{}, errors.New("copy: connection refused")
	}
	return c.synced, nil
}

type offlineRig struct {
	home *fakeHome
	cp   *fakeCopy
	r    *Resolver
	h    http.Handler
	now  time.Time
}

// newOfflineRig builds a node's front over the resolver: a dispatcher with `local`
// routes (the stream sessions, an allowlisted version), an `any` read and write, `home`
// routes (proxied); the auth gate answers 401 to an anonymous request. withCopy false: a
// node without a copy.
func newOfflineRig(t *testing.T, withCopy bool) *offlineRig {
	t.Helper()
	g := &offlineRig{home: &fakeHome{}, now: time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC)}
	g.home.expires = g.now.Add(24 * time.Hour)
	g.home.srv = httptest.NewServer(http.HandlerFunc(g.home.serveAPI))
	t.Cleanup(g.home.srv.Close)
	g.r = &Resolver{Link: g.home, Now: func() time.Time { return g.now }}
	if withCopy {
		g.cp = &fakeCopy{synced: g.now, expires: g.now.Add(24 * time.Hour)}
		g.r.Local = g.cp
		g.r.Sessions = &LocalSessions{Path: filepath.Join(t.TempDir(), LocalSessionsFile), Now: func() time.Time { return g.now }}
	}

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
	routes.Post("/api/videos/{id}/rating", ok)
	routes.Get("/api/system/version", ok)
	routes.Post("/api/stream/sessions", ok)
	routes.Delete("/api/stream/sessions/{id}", ok)
	routes.Get("/api/auth/me", ok)
	routes.Post("/api/auth/login", ok)
	routes.Post("/api/auth/logout", ok)
	routes.Get("/api/settings", ok)
	var table placement.Table
	table = append(table, placement.Exact(placement.Local, nil,
		"/api/system/version", "/api/stream/sessions", "/api/stream/sessions/{id}")...)
	table = append(table, placement.Exact(placement.Any, nil, "/api/videos", "/api/videos/{id}/rating")...)
	table = append(table, placement.Prefixes(placement.Home, "/api/auth/", "/api/settings")...)
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
	cookies  []*http.Cookie
}

// do sends one request; cred is a session id, "pat_…" for a token, "" for none.
func (g *offlineRig) do(method, path, cred string) answer {
	return g.send(method, path, cred, "")
}

func (g *offlineRig) send(method, path, cred, body string) answer {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	switch {
	case strings.HasPrefix(cred, "pat_"):
		req.Header.Set("Authorization", "Bearer "+cred)
	case cred != "":
		req.AddCookie(&http.Cookie{Name: CookieSession, Value: cred})
	}
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	a := answer{code: rec.Code, marked: rec.Header().Get(HomeStateHeader) == "unreachable",
		cookies: rec.Result().Cookies()}
	var m map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &m) == nil {
		a.body = m
		a.problem, _ = m["code"].(string)
		if v, ok := m["readable"].(bool); ok {
			a.readable = &v
		}
	}
	return a
}

// signIn posts a sign-in; the node-local session's cookie value on success.
func (g *offlineRig) signIn(t *testing.T, user, pw string) (answer, string) {
	t.Helper()
	a := g.send(http.MethodPost, LoginPath, "", `{"username":"`+user+`","password":"`+pw+`"}`)
	for _, c := range a.cookies {
		if c.Name == CookieSession {
			return a, c.Value
		}
	}
	return a, ""
}

// goDown: the home answers (Watch's probe every 5 s) for `after` more, then goes away;
// the copy has applied everything up to then.
func (g *offlineRig) goDown(after time.Duration) {
	g.now = g.now.Add(after)
	g.r.answered()
	if g.cp != nil {
		g.cp.set(func(c *fakeCopy) { c.synced = g.now })
	}
	g.home.setDown(true)
}

// pastGrace moves past the grace period, so the home is no longer asked per request.
func (g *offlineRig) pastGrace() {
	g.now = g.now.Add(DefaultGrace + time.Second)
	g.do(http.MethodGet, "/api/videos", "s1") // a call the home does not answer starts the clock
	g.now = g.now.Add(DefaultGrace + time.Second)
}

func (a answer) refusedRead() bool {
	return a.code == http.StatusServiceUnavailable && a.problem == "home_unreachable" && a.readable != nil && !*a.readable
}

func (a answer) refusedChange() bool {
	return a.code == http.StatusServiceUnavailable && a.problem == "home_unreachable" && a.readable == nil
}

func TestOutageServesFromTheCopy(t *testing.T) {
	g := newOfflineRig(t, true)
	if a := g.do(http.MethodGet, "/api/videos", "s1"); a.code != http.StatusOK || a.marked {
		t.Fatalf("home up: %+v", a)
	}
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
	// a change through the home's database waits for it
	if a := g.do(http.MethodPost, "/api/videos/v1/rating", "s1"); !a.refusedChange() {
		t.Fatalf("a change in an outage: %+v, want 503 home_unreachable", a)
	}
	// the copy says no: anonymous, as a refusal of the home's would be
	for _, path := range []string{"/api/videos", "/api/stream/sessions"} {
		method := http.MethodGet
		if path == "/api/stream/sessions" {
			method = http.MethodPost
		}
		if a := g.do(method, path, "signed-out"); a.code != http.StatusUnauthorized || !a.marked {
			t.Fatalf("%s with a credential the copy says is not live: %+v, want anonymous (401)", path, a)
		}
	}
	if a := g.do(http.MethodGet, "/api/system/version", ""); a.code != http.StatusOK {
		t.Fatalf("an allowlisted local route: %+v", a)
	}
	// the session probe: the home is tried, does not answer, the copy answers
	if a := g.do(http.MethodGet, MePath, "s1"); a.code != http.StatusOK || !a.marked {
		t.Fatalf("/api/auth/me in the grace: %+v", a)
	} else if u, _ := a.body["user"].(map[string]any); u["id"] != "u1" || u["username"] != "amal" || a.body["session"] != "home" {
		t.Fatalf("/api/auth/me body: %v", a.body)
	}

	// past the grace: `home` routes are refused at once, the probe answered here
	g.pastGrace()
	before := g.home.proxiedCount()
	if a := g.do(http.MethodGet, "/api/settings", "s1"); !a.refusedChange() {
		t.Fatalf("a home route past the grace: %+v, want 503 home_unreachable", a)
	}
	if a := g.do(http.MethodGet, MePath, "s1"); a.code != http.StatusOK || !a.marked {
		t.Fatalf("/api/auth/me past the grace: %+v", a)
	}
	for _, cred := range []string{"", "signed-out"} {
		if a := g.do(http.MethodGet, MePath, cred); a.code != http.StatusUnauthorized || !a.marked {
			t.Fatalf("/api/auth/me (%q) past the grace: %+v, want 401 marked (the app shows its sign-in)", cred, a)
		}
	}
	if g.home.proxiedCount() != before {
		t.Fatalf("the home was tried past the grace")
	}
	// the session's own expiry, in the copy, still counts
	g.cp.set(func(c *fakeCopy) { c.expires = g.now })
	if a := g.do(http.MethodGet, "/api/videos", "s1"); a.code != http.StatusUnauthorized {
		t.Fatalf("an expired session: %+v, want anonymous (401)", a)
	}
	g.cp.set(func(c *fakeCopy) { c.expires = g.now.Add(time.Hour) })

	// the home answers again (Watch's probe records it): back to normal
	g.home.setDown(false)
	g.r.answered()
	if a := g.do(http.MethodGet, "/api/videos", "s1"); a.code != http.StatusOK || a.marked {
		t.Fatalf("home back: %+v", a)
	}
}

func TestOutageWithoutACopy(t *testing.T) {
	g := newOfflineRig(t, false)
	g.do(http.MethodGet, "/api/videos", "s1")
	g.goDown(PositiveTTL + time.Second)
	if a := g.do(http.MethodGet, "/api/videos", "s1"); !a.refusedRead() {
		t.Fatalf("a read with no copy: %+v, want 503 readable false", a)
	}
	// this machine's own routes stay up, anonymously: a cookie or a token does not count
	for _, cred := range []string{"s1", "pat_1"} {
		if a := g.do(http.MethodPost, "/api/stream/sessions", cred); a.code != http.StatusUnauthorized || !a.marked {
			t.Fatalf("playback (%s) with no copy: %+v, want anonymous (401) marked", cred, a)
		}
	}
	g.pastGrace()
	if a := g.do(http.MethodGet, MePath, "s1"); !a.refusedRead() {
		t.Fatalf("/api/auth/me with no copy: %+v, want 503 readable false", a)
	}
	if a, _ := g.signIn(t, "amal", "pw-amal"); !a.refusedChange() {
		t.Fatalf("a sign-in with no copy: %+v, want 503 home_unreachable", a)
	}
}

// The copy answers only while it was current when the home was last heard: one that
// stopped following long before might miss a sign-out or a password change.
func TestACopyBehindTheHomeDoesNotAnswer(t *testing.T) {
	g := newOfflineRig(t, true)
	g.goDown(time.Minute)
	g.cp.set(func(c *fakeCopy) { c.synced = g.now.Add(-DefaultCopyMaxLag - time.Second) })
	g.pastGrace()
	if a := g.do(http.MethodGet, "/api/videos", "s1"); !a.refusedRead() {
		t.Fatalf("a read on a copy behind: %+v, want 503 readable false", a)
	}
	if a := g.do(http.MethodPost, "/api/stream/sessions", "s1"); a.code != http.StatusUnauthorized {
		t.Fatalf("playback on a copy behind: %+v, want anonymous (401)", a)
	}
	if a, _ := g.signIn(t, "amal", "pw-amal"); !a.refusedRead() {
		t.Fatalf("a sign-in on a copy behind: %+v, want 503 readable false", a)
	}
	// a little behind is current; the outage's length does not count
	g.cp.set(func(c *fakeCopy) {
		c.synced = g.r.lastOK.Add(-DefaultCopyMaxLag + time.Minute)
		c.expires = g.now.Add(72 * time.Hour)
	})
	g.now = g.now.Add(48 * time.Hour)
	if a := g.do(http.MethodGet, "/api/videos", "s1"); a.code != http.StatusOK || !a.marked {
		t.Fatalf("a read on a current copy two days into the outage: %+v", a)
	}
	// a copy that does not answer cannot say
	g.cp.set(func(c *fakeCopy) { c.broken = true })
	if a := g.do(http.MethodGet, "/api/videos", "s1"); !a.refusedRead() {
		t.Fatalf("a read with the copy down: %+v, want 503 readable false", a)
	}
}

// A node restarted in the outage measures its copy against when the home last answered
// (kept on its disk), not against its own start.
func TestRestartInAnOutageKeepsWhenTheHomeLastAnswered(t *testing.T) {
	g := newOfflineRig(t, true)
	path := filepath.Join(t.TempDir(), HomeSeenFile)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { g.r.KeepHomeSeen(ctx, path); close(done) }()
	g.goDown(time.Minute)
	seen := g.now
	cancel()
	<-done // written once more as it stops

	later := seen.Add(6 * time.Hour)
	cp := &fakeCopy{synced: seen.Add(-time.Minute), expires: later.Add(time.Hour)}
	fresh := &Resolver{Link: g.home, Local: cp, Now: func() time.Time { return later }}
	if fresh.CopyCurrent(context.Background()) {
		t.Fatal("a restarted node that kept nothing trusted a copy six hours behind its own clock")
	}
	fresh.LoadHomeSeen(path)
	if !fresh.lastOK.Equal(seen) {
		t.Fatalf("kept %v, want %v", fresh.lastOK, seen)
	}
	if !fresh.CopyCurrent(context.Background()) {
		t.Fatal("the copy, current when the home was last heard, does not answer after a restart")
	}
}

func TestSignInOnTheCopyWhileTheHomeIsAway(t *testing.T) {
	g := newOfflineRig(t, true)
	g.goDown(time.Minute)
	g.pastGrace()
	if a, _ := g.signIn(t, "amal", "wrong"); a.code != http.StatusUnauthorized {
		t.Fatalf("a wrong password: %+v, want 401", a)
	}
	a, id := g.signIn(t, "amal", "pw-amal")
	if a.code != http.StatusOK || !a.marked || !strings.HasPrefix(id, LocalSessionPrefix) || a.body["session"] != "node" {
		t.Fatalf("a sign-in on the copy: %+v (cookie %q)", a, id)
	}
	var csrf *http.Cookie
	for _, c := range a.cookies {
		if c.Name == CookieSession && !c.HttpOnly {
			t.Fatal("the node-local session cookie is not HttpOnly")
		}
		if c.Name == CookieCSRF {
			csrf = c
		}
	}
	if csrf == nil || csrf.Value == "" {
		t.Fatal("no CSRF cookie")
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/videos"},
		{http.MethodPost, "/api/stream/sessions"},
	} {
		if a := g.do(c.method, c.path, id); a.code != http.StatusOK || !a.marked {
			t.Fatalf("%s %s on a node-local session: %+v", c.method, c.path, a)
		}
	}
	if a := g.do(http.MethodGet, MePath, id); a.code != http.StatusOK || a.body["session"] != "node" {
		t.Fatalf("/api/auth/me on a node-local session: %+v", a)
	}
	if a := g.do(http.MethodPost, "/api/videos/v1/rating", id); !a.refusedChange() {
		t.Fatalf("a change on a node-local session with the home away: %+v, want 503", a)
	}
	// a native app signs in with the home
	req := httptest.NewRequest(http.MethodPost, LoginPath, strings.NewReader(`{"username":"amal","password":"pw-amal"}`))
	req.Header.Set("X-Maktaba-Client", "native")
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a native sign-in on the copy: %d, want 503", rec.Code)
	}
	// an account waiting on a password change waits for the home
	if a, id := g.signIn(t, "badr", "pw-badr"); a.code != http.StatusServiceUnavailable || id != "" {
		t.Fatalf("a must-change account: %+v", a)
	}

	// the home is back: reads go on for the grace, a change asks for a sign-in there
	g.home.setDown(false)
	g.r.answered()
	if a := g.do(http.MethodGet, "/api/videos", id); a.code != http.StatusOK || a.marked {
		t.Fatalf("a read on a node-local session with the home back: %+v", a)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/videos/v1/rating"},
		{http.MethodGet, "/api/settings"}, // the home knows nothing of it
	} {
		if a := g.do(c.method, c.path, id); a.code != http.StatusUnauthorized || a.problem != "signin_required" {
			t.Fatalf("%s %s on a node-local session with the home back: %+v, want 401 signin_required", c.method, c.path, a)
		}
	}
	if a := g.do(http.MethodGet, MePath, id); a.code != http.StatusOK || a.marked {
		t.Fatalf("/api/auth/me on a node-local session with the home back: %+v", a)
	}
	g.now = g.now.Add(DefaultLocalSessionGrace + time.Second)
	if a := g.do(http.MethodGet, "/api/videos", id); a.code != http.StatusUnauthorized {
		t.Fatalf("a node-local session past the grace: %+v, want anonymous (401)", a)
	}
	if a := g.do(http.MethodGet, MePath, id); a.code != http.StatusUnauthorized {
		t.Fatalf("/api/auth/me past the grace: %+v, want 401", a)
	}
}

func TestSignOutOfANodeLocalSession(t *testing.T) {
	g := newOfflineRig(t, true)
	g.goDown(time.Minute)
	g.pastGrace()
	_, id := g.signIn(t, "amal", "pw-amal")
	if a := g.do(http.MethodPost, LogoutPath, id); a.code != http.StatusNoContent {
		t.Fatalf("sign-out: %+v", a)
	}
	if a := g.do(http.MethodGet, "/api/videos", id); a.code != http.StatusUnauthorized {
		t.Fatalf("after the sign-out: %+v, want anonymous (401)", a)
	}
	// with the home back too, the sign-out is this machine's
	_, id = g.signIn(t, "amal", "pw-amal")
	g.home.setDown(false)
	g.r.answered()
	before := g.home.proxiedCount()
	if a := g.do(http.MethodPost, LogoutPath, id); a.code != http.StatusNoContent || g.home.proxiedCount() != before {
		t.Fatalf("sign-out with the home back: %+v (proxied %d)", a, g.home.proxiedCount()-before)
	}
}

func TestOfflineSignInLocksAfterRepeatedFailures(t *testing.T) {
	g := newOfflineRig(t, true)
	g.goDown(time.Minute)
	g.pastGrace()
	for i := 0; i < lockoutThreshold; i++ {
		if a, _ := g.signIn(t, "Amal", "wrong"); a.code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %+v", i+1, a)
		}
	}
	if a, id := g.signIn(t, "amal", "pw-amal"); a.code != http.StatusLocked || id != "" {
		t.Fatalf("the right password after %d failures: %+v, want 423", lockoutThreshold, a)
	}
	g.now = g.now.Add(lockoutWindow + time.Second)
	if a, id := g.signIn(t, "amal", "pw-amal"); a.code != http.StatusOK || id == "" {
		t.Fatalf("after the window: %+v", a)
	}
}

// A change the middleware sees outside a local route is refused (every write of a
// node is `home` or `any`).
func TestOfflineAChangeOutsideLocalRoutesIsRefused(t *testing.T) {
	g := newOfflineRig(t, true)
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
