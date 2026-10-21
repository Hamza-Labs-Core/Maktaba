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
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homelink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homeproxy"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/placement"
)

func init() { signInFloor = 0 }

// s1's CSRF token (the home's answers and the copy's give its hash).
const s1CSRF = "csrf-s1"

var s1CSRFHash = func() string { s := sha256.Sum256([]byte(s1CSRF)); return hex.EncodeToString(s[:]) }()

// fakeHome answers introspection until it goes down; its API (behind the proxy) then
// drops every connection.
type fakeHome struct {
	mu       sync.Mutex
	down     bool
	expires  time.Time // the answers' expires_at
	proxied  int
	srv      *httptest.Server
	signOuts []delegation.SignOutRequest // what the node handed over
	jwks     []byte                      // nil: no keys
	hang     bool                        // down as a machine switched off: nothing answers
	// asked, when set, holds an introspection until it is closed
	asked chan struct{}
	// what the last proxied request carried: its cookies and its delegated session
	cookie, delegated string
}

func (f *fakeHome) setDown(d bool) { f.mu.Lock(); f.down = d; f.mu.Unlock() }
func (f *fakeHome) isDown() bool   { f.mu.Lock(); defer f.mu.Unlock(); return f.down }

func (f *fakeHome) Transport() http.RoundTripper { return http.DefaultTransport }
func (f *fakeHome) HomeURL() string              { return f.srv.URL }
func (f *fakeHome) Introspect(_ context.Context, req delegation.IntrospectRequest) (*delegation.IntrospectAnswer, error) {
	if f.isDown() {
		return nil, homelink.ErrUnreachable
	}
	f.mu.Lock()
	hold := f.asked
	f.mu.Unlock()
	if hold != nil {
		<-hold
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
	return &delegation.IntrospectAnswer{Source: src, CSRFSHA256: s1CSRFHash, ExpiresAt: f.expires,
		Principal: delegation.Principal{UserID: "u1", Username: "amal", Libraries: []string{"lib1"}}}, nil
}
func (f *fakeHome) Exchange(context.Context, delegation.ExchangeRequest) (*delegation.ExchangeAnswer, error) {
	return nil, homelink.ErrUnauthorized
}
func (f *fakeHome) JWKS(context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.jwks != nil {
		return f.jwks, nil
	}
	return []byte(`{"keys":[]}`), nil
}
func (f *fakeHome) Self(context.Context) (json.RawMessage, error) {
	if f.isDown() {
		return nil, errors.New("down")
	}
	return json.RawMessage(`{}`), nil
}
func (f *fakeHome) Subscribe(func(homelink.Event)) {}
func (f *fakeHome) HasKey() bool                   { return true }
func (f *fakeHome) SignOut(_ context.Context, req delegation.SignOutRequest) (*delegation.SignOutAnswer, error) {
	if f.isDown() {
		return nil, homelink.ErrUnreachable
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signOuts = append(f.signOuts, req)
	return &delegation.SignOutAnswer{Revoked: len(req.SessionSHA256)}, nil
}

func (f *fakeHome) serveAPI(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	hang := f.hang
	f.mu.Unlock()
	if hang {
		<-r.Context().Done() // never answered
		return
	}
	if f.isDown() {
		if c, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = c.Close() // the request dies on the wire: the proxy's error path
		}
		return
	}
	f.mu.Lock()
	f.proxied++
	f.cookie, f.delegated = r.Header.Get("Cookie"), r.Header.Get(delegation.HeaderDelegatedSession)
	f.mu.Unlock()
	w.WriteHeader(http.StatusTeapot)
}

func (f *fakeHome) seen() (cookie, delegated string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cookie, f.delegated
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
		return &delegation.IntrospectAnswer{Principal: copyUsers["u1"], Source: "cookie", CSRFSHA256: s1CSRFHash,
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
	// csrf is each session's CSRF token (sent with a change)
	csrf map[string]string
}

// newOfflineRig builds a node's front over the resolver: a dispatcher with `local`
// routes (the stream sessions, an allowlisted version), an `any` read and write, `home`
// routes (proxied); the auth gate answers 401 to an anonymous request. withCopy false: a
// node without a copy.
func newOfflineRig(t *testing.T, withCopy bool) *offlineRig {
	t.Helper()
	g := &offlineRig{home: &fakeHome{}, now: time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC),
		csrf: map[string]string{"s1": s1CSRF}}
	g.home.expires = g.now.Add(24 * time.Hour)
	g.home.srv = httptest.NewServer(http.HandlerFunc(g.home.serveAPI))
	t.Cleanup(g.home.srv.Close)
	g.r = &Resolver{Link: g.home, Now: func() time.Time { return g.now }}
	if withCopy {
		g.cp = &fakeCopy{synced: g.now, expires: g.now.Add(24 * time.Hour)}
		g.r.Local = g.cp
		g.r.Sessions = &LocalSessions{Path: filepath.Join(t.TempDir(), LocalSessionsFile), Now: func() time.Time { return g.now }}
	}
	g.r.SignOuts = &SignOuts{Path: filepath.Join(t.TempDir(), SignOutsFile), Now: func() time.Time { return g.now }}

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
	routes.Post("/api/auth/logout-all", ok)
	routes.Get("/api/settings", ok)
	var table placement.Table
	table = append(table, placement.Exact(placement.Local, nil,
		"/api/system/version", "/api/stream/sessions", "/api/stream/sessions/{id}")...)
	table = append(table, placement.Exact(placement.Any, nil, "/api/videos", "/api/videos/{id}/rating")...)
	table = append(table, placement.Prefixes(placement.Home, "/api/auth/", "/api/settings")...)
	g.h = &placement.Dispatcher{Routes: routes, Table: table, Local: g.r.Middleware(gate),
		Proxy: g.r.Home(homeproxy.NewWithFallback(g.home, nil, g.r.Fallback))}
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
	case strings.HasPrefix(cred, "jwt "):
		req.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(cred, "jwt "))
	case cred != "":
		req.AddCookie(&http.Cookie{Name: CookieSession, Value: cred})
		if tok := g.csrf[cred]; tok != "" && !readOnly(method) {
			req.AddCookie(&http.Cookie{Name: CookieCSRF, Value: tok})
			req.Header.Set(CSRFHeader, tok)
		}
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
	id, csrf := "", ""
	for _, c := range a.cookies {
		switch c.Name {
		case CookieSession:
			id = c.Value
		case CookieCSRF:
			csrf = c.Value
		}
	}
	if id != "" {
		g.csrf[id] = csrf
	}
	return a, id
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
	if a, id := g.signIn(t, "badr", "pw-badr"); a.code != http.StatusServiceUnavailable || id != "" ||
		a.problem != "password_change_waits" {
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

// With the home back, a person signed in on this machine signs in with the home: the
// sign-in goes there without this machine's cookies, and the node-local session ends. A
// change that asks for a sign-in clears the node-local cookie.
func TestASignInWithTheHomeBackEndsTheNodeLocalOne(t *testing.T) {
	g := newOfflineRig(t, true)
	g.goDown(time.Minute)
	g.pastGrace()
	_, id := g.signIn(t, "amal", "pw-amal")
	g.home.setDown(false)
	g.r.answered()
	cleared := func(a answer) bool {
		for _, c := range a.cookies {
			if c.Name == CookieSession && c.Value == "" && c.MaxAge < 0 {
				return true
			}
		}
		return false
	}
	if a := g.do(http.MethodPost, "/api/videos/v1/rating", id); a.problem != "signin_required" || !cleared(a) {
		t.Fatalf("a change on a node-local session with the home back: %+v, want signin_required and the cookie cleared", a)
	}
	if a := g.do(http.MethodGet, "/api/settings", id); a.problem != "signin_required" || !cleared(a) {
		t.Fatalf("a home page on a node-local session with the home back: %+v", a)
	}
	before := g.home.proxiedCount()
	a := g.send(http.MethodPost, LoginPath, id, `{"username":"amal","password":"pw-amal"}`)
	if a.code != http.StatusTeapot || g.home.proxiedCount() != before+1 {
		t.Fatalf("a sign-in with the home back: %+v, want the home's own answer", a)
	}
	if c, d := g.home.seen(); strings.Contains(c, LocalSessionPrefix) || strings.Contains(c, CookieCSRF) || d != "" {
		t.Fatalf("the home got this machine's session: cookie %q, delegated %q", c, d)
	}
	if a := g.do(http.MethodGet, "/api/videos", id); a.code != http.StatusUnauthorized {
		t.Fatalf("the node-local session after the sign-in with the home: %+v, want ended (401)", a)
	}
	if a := g.do(http.MethodGet, "/api/videos", id); !cleared(a) {
		t.Fatalf("an ended node-local session's cookie is not cleared: %+v", a)
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

// Inside the grace a sign-in still goes to the home; when the home does not answer it,
// this machine answers it from its copy (the proxy may have read the body already).
func TestSignInInsideTheGraceFallsBackToTheCopy(t *testing.T) {
	g := newOfflineRig(t, true)
	g.goDown(time.Minute)
	if g.r.Unreachable() {
		t.Fatal("past the grace already")
	}
	a, id := g.signIn(t, "amal", "pw-amal")
	if a.code != http.StatusOK || !strings.HasPrefix(id, LocalSessionPrefix) {
		t.Fatalf("a sign-in inside the grace, the home not answering: %+v", a)
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

func (a answer) cleared() bool {
	n := 0
	for _, c := range a.cookies {
		if (c.Name == CookieSession || c.Name == CookieCSRF) && c.MaxAge < 0 {
			n++
		}
	}
	return n == 2
}

// #145: a sign-out of a home session while the home is away is real on this machine at
// once (cookies cleared, the session denied here though the copy still says it is live),
// kept by the session's hash only, and handed to the home once it answers; until the
// home has it, nothing of that session is sent there.
func TestSignOutOfAHomeSessionInAnOutage(t *testing.T) {
	g := newOfflineRig(t, true)
	g.goDown(time.Minute)
	g.pastGrace()
	if a := g.do(http.MethodGet, "/api/videos", "s1"); a.code != http.StatusOK {
		t.Fatalf("before the sign-out: %+v", a)
	}
	// without the CSRF token: refused, still signed in
	delete(g.csrf, "s1")
	if a := g.do(http.MethodPost, LogoutPath, "s1"); a.code != http.StatusForbidden {
		t.Fatalf("a sign-out without the CSRF token: %+v, want 403", a)
	}
	g.csrf["s1"] = s1CSRF
	if a := g.do(http.MethodPost, LogoutPath, "s1"); a.code != http.StatusNoContent || !a.marked || !a.cleared() {
		t.Fatalf("a sign-out in the outage: %+v, want 204 with the cookies cleared", a)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/videos"}, {http.MethodPost, "/api/stream/sessions"}, {http.MethodGet, MePath},
	} {
		if a := g.do(c.method, c.path, "s1"); a.code != http.StatusUnauthorized {
			t.Fatalf("%s %s after the sign-out: %+v, want 401", c.method, c.path, a)
		}
	}
	raw, err := os.ReadFile(g.r.SignOuts.Path)
	if err != nil || strings.Contains(string(raw), `"s1"`) || !strings.Contains(string(raw), SessionKey("s1")) {
		t.Fatalf("the kept sign-out: %s %v (the hash, never the id)", raw, err)
	}
	// a restart in the outage keeps it
	again := &SignOuts{Path: g.r.SignOuts.Path, Now: g.r.SignOuts.Now}
	if err := again.Load(); err != nil || !again.denied(SessionKey("s1"), "u1") || again.Waiting() != 1 {
		t.Fatalf("after a restart: %v waiting %d", err, again.Waiting())
	}
	// the home is back: nothing of that session reaches it before it is told
	g.home.setDown(false)
	g.r.answered()
	before := g.home.proxiedCount()
	if a := g.do(http.MethodGet, "/api/settings", "s1"); a.code != http.StatusUnauthorized || g.home.proxiedCount() != before {
		t.Fatalf("a home route on the signed-out session: %+v (proxied %d)", a, g.home.proxiedCount()-before)
	}
	if a := g.do(http.MethodGet, "/api/videos", "s1"); a.code != http.StatusUnauthorized {
		t.Fatalf("a read with the home back, before it is told: %+v", a)
	}
	g.r.sendSignOuts(context.Background())
	if len(g.home.signOuts) != 1 || len(g.home.signOuts[0].SessionSHA256) != 1 ||
		g.home.signOuts[0].SessionSHA256[0] != SessionKey("s1") || g.r.SignOuts.Waiting() != 0 {
		t.Fatalf("handed to the home: %+v (waiting %d)", g.home.signOuts, g.r.SignOuts.Waiting())
	}
	g.r.sendSignOuts(context.Background())
	if len(g.home.signOuts) != 1 {
		t.Fatal("handed over twice")
	}
	// another session of the same user is not touched
	g.csrf["s2"] = "x"
	if g.r.SignOuts.denied(SessionKey("s2"), "u1") {
		t.Fatal("a sign-out denies the user's other sessions")
	}
}

// Inside the grace the sign-out goes to the home; when the home does not answer it, this
// machine signs the person out (the proxy's fallback).
func TestSignOutInsideTheGraceFallsBackToThisMachine(t *testing.T) {
	g := newOfflineRig(t, true)
	g.goDown(time.Minute)
	if a := g.do(http.MethodPost, LogoutPath, "s1"); a.code != http.StatusNoContent || !a.cleared() {
		t.Fatalf("a sign-out the home does not answer: %+v", a)
	}
	if !g.r.SignOuts.denied(SessionKey("s1"), "u1") {
		t.Fatal("not kept")
	}
}

// "Sign out everywhere" in an outage: every node-local session of the user ends, their
// home sessions are denied here until the home took it (then a session it says is live
// was made since), and the home is told to end those made before.
func TestSignOutEverywhereInAnOutage(t *testing.T) {
	g := newOfflineRig(t, true)
	g.goDown(time.Minute)
	g.pastGrace()
	_, local := g.signIn(t, "amal", "pw-amal")
	at := g.now
	if a := g.do(http.MethodPost, LogoutAllPath, "s1"); a.code != http.StatusNoContent || !a.cleared() {
		t.Fatalf("sign out everywhere: %+v", a)
	}
	if a := g.do(http.MethodGet, "/api/videos", local); a.code != http.StatusUnauthorized {
		t.Fatalf("the user's node-local session after it: %+v", a)
	}
	if !g.r.SignOuts.denied(SessionKey("other-session-of-u1"), "u1") {
		t.Fatal("another home session of the user is not denied while the home has not taken it")
	}
	g.home.setDown(false)
	g.r.answered()
	g.r.sendSignOuts(context.Background())
	var all *delegation.SignOutRequest
	for i, req := range g.home.signOuts {
		if req.UserID == "u1" {
			all = &g.home.signOuts[i]
		}
	}
	if all == nil || all.Before == nil || !all.Before.Equal(at) {
		t.Fatalf("handed to the home: %+v", g.home.signOuts)
	}
	if g.r.SignOuts.denied(SessionKey("a-session-made-since"), "u1") {
		t.Fatal("a session the home made after it is denied")
	}
}

// #145: a revocation the home pushes while an introspection is under way is not undone
// by caching that answer, and the copy's answers honour it until the copy has it.
func TestARevocationIsNotUndoneByAnAnswerInFlight(t *testing.T) {
	g := newOfflineRig(t, true)
	hold := make(chan struct{})
	g.home.mu.Lock()
	g.home.asked = hold
	g.home.mu.Unlock()
	done := make(chan answer)
	go func() { done <- g.do(http.MethodGet, "/api/videos", "s1") }()
	time.Sleep(50 * time.Millisecond) // the introspection is waiting on the home
	g.r.Evict(delegation.Revoked{SessionSHA256: []string{SessionKey("s1")}})
	close(hold)
	<-done
	g.home.mu.Lock()
	g.home.asked = nil
	g.home.mu.Unlock()
	if _, ok := g.r.get(SessionKey("s1")); ok {
		t.Fatal("the answer asked for before the revocation was cached")
	}
	// the home goes away before the copy has the revocation: the copy is not believed
	g.goDown(time.Second)
	g.pastGrace()
	if a := g.do(http.MethodGet, "/api/videos", "s1"); a.code != http.StatusUnauthorized {
		t.Fatalf("a session the home revoked, on the copy that does not have it yet: %+v", a)
	}
	if a := g.do(http.MethodGet, "/api/videos", "pat_1"); a.code != http.StatusOK {
		t.Fatalf("another credential of the user: %+v", a)
	}
}

// #145: a bearer access token is held to the outage's rules: with the home away its user
// is checked on the copy (a user the copy does not have is no one), and a change through
// the home's database is refused as for any other credential.
func TestABearerTokenInAnOutageIsCheckedOnTheCopy(t *testing.T) {
	g := newOfflineRig(t, true)
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	set := keys.NewSet(0)
	set.Replace(&keys.Key{KID: "k1", Private: priv, Public: &priv.PublicKey})
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1",
		"n": base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes())}}})
	g.home.jwks = jwks
	sign := func(user string) string {
		tok, err := jwt.Sign(set, jwt.Claims{Aud: "api", Sub: user, Usr: user, Exp: time.Now().Add(time.Hour).Unix()})
		if err != nil {
			t.Fatal(err)
		}
		return "jwt " + tok
	}
	if a := g.do(http.MethodGet, "/api/videos", sign("u1")); a.code != http.StatusOK || a.marked {
		t.Fatalf("a bearer token with the home up: %+v", a)
	}
	g.goDown(time.Minute)
	g.pastGrace()
	if a := g.do(http.MethodGet, "/api/videos", sign("u1")); a.code != http.StatusOK || !a.marked {
		t.Fatalf("a bearer token in the outage: %+v, want 200 marked", a)
	}
	if a := g.do(http.MethodGet, "/api/videos", sign("gone")); a.code != http.StatusUnauthorized {
		t.Fatalf("a bearer token of a user the copy does not have: %+v, want anonymous (401)", a)
	}
	if a := g.do(http.MethodPost, "/api/videos/v1/rating", sign("u1")); !a.refusedChange() {
		t.Fatalf("a change on a bearer token in the outage: %+v, want 503 home_unreachable", a)
	}
	g.cp.set(func(c *fakeCopy) { c.synced = g.now.Add(-time.Hour) }) // a copy that stopped following
	if a := g.do(http.MethodGet, "/api/videos", sign("u1")); !a.refusedRead() {
		t.Fatalf("a bearer token on a copy behind the home: %+v, want 503 readable false", a)
	}
}

// #145: past the grace a page the home serves gets the notice while this machine can
// still serve the person's reads, and the full page when it cannot (its copy behind).
func TestAHomePagePastTheWindowIsTheFullPage(t *testing.T) {
	g := newOfflineRig(t, true)
	g.goDown(time.Minute)
	g.pastGrace()
	if a := g.do(http.MethodGet, "/api/settings", "s1"); a.code != http.StatusServiceUnavailable || a.readable != nil {
		t.Fatalf("a home page with the copy current: %+v, want the notice (readable unset)", a)
	}
	g.cp.set(func(c *fakeCopy) { c.synced = g.now.Add(-time.Hour) })
	if a := g.do(http.MethodGet, "/api/settings", "s1"); !a.refusedRead() {
		t.Fatalf("a home page with the copy behind: %+v, want the full page (readable false)", a)
	}
}

func TestDownForIsNotOneUnansweredProbe(t *testing.T) {
	g := newOfflineRig(t, true)
	g.r.unanswered()
	if g.r.DownFor(10 * time.Second) {
		t.Fatal("down at the first unanswered probe")
	}
	g.now = g.now.Add(10 * time.Second)
	if !g.r.DownFor(10 * time.Second) {
		t.Fatal("not down after 10 s")
	}
	g.r.answered()
	if g.r.DownFor(0) {
		t.Fatal("down once the home answered")
	}
}

// #157: a home switched off leaves what is sent to it hanging: inside the grace, once it
// stopped answering its probes, the session probe, a sign-in and a sign-out are
// answered here at once, not after waiting on it.
func TestAHangingHomeIsNotWaitedOnForWhatThisMachineAnswers(t *testing.T) {
	g := newOfflineRig(t, true)
	g.goDown(PositiveTTL + time.Second)
	g.home.mu.Lock()
	g.home.hang = true
	g.home.mu.Unlock()
	g.r.unanswered() // a probe the home did not answer
	if g.r.Unreachable() {
		t.Fatal("past the grace already")
	}
	start := time.Now()
	if a := g.do(http.MethodGet, MePath, "s1"); a.code != http.StatusOK || !a.marked {
		t.Fatalf("/api/auth/me with the home hanging: %+v", a)
	}
	if a, id := g.signIn(t, "amal", "pw-amal"); a.code != http.StatusOK || !strings.HasPrefix(id, LocalSessionPrefix) {
		t.Fatalf("a sign-in with the home hanging: %+v", a)
	}
	if a := g.do(http.MethodPost, LogoutPath, "s1"); a.code != http.StatusNoContent {
		t.Fatalf("a sign-out with the home hanging: %+v", a)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("waited %s on a home that does not answer", d)
	}
}

// One call the home does not answer is not an outage, and a caller that went away is not
// one either: the outage is the home silent for a probe's interval.
func TestOneMissIsNotAnOutage(t *testing.T) {
	g := newOfflineRig(t, true)
	g.home.setDown(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _ = g.r.resolve(ctx, "k-gone", delegation.IntrospectRequest{Session: "s-gone"})
	if ok, _ := g.r.Reachability(); !ok || g.r.DownFor(0) {
		t.Fatal("a caller that went away counted as the home not answering")
	}
	_, _, _ = g.r.resolve(context.Background(), "k-miss", delegation.IntrospectRequest{Session: "s-miss"})
	if ok, _ := g.r.Reachability(); !ok || g.r.Down() || !g.r.DownFor(0) {
		t.Fatal("one call the home did not answer: reported as an outage")
	}
	g.now = g.now.Add(ProbeEvery)
	if ok, since := g.r.Reachability(); ok || since == nil || !g.r.Down() {
		t.Fatal("the home silent for a probe's interval is not reported")
	}
}

// A person the home signs out everywhere, resets or locks loses their sign-ins on this
// machine too: the pushed revocation ends them; and one whose password must now change
// (an admin reset it, as the copy says) is not let in on a node-local session.
func TestARevocationEndsTheNodeLocalSignIns(t *testing.T) {
	g := newOfflineRig(t, true)
	g.goDown(time.Minute)
	g.pastGrace()
	_, id := g.signIn(t, "amal", "pw-amal")
	if a := g.do(http.MethodGet, "/api/videos", id); a.code != http.StatusOK {
		t.Fatalf("signed in on this machine: %+v", a)
	}
	g.r.OnEvent(homelink.Event{Kind: "revoked", Revoked: &delegation.Revoked{UserID: "u1"}})
	if a := g.do(http.MethodGet, "/api/videos", id); a.code != http.StatusUnauthorized {
		t.Fatalf("after the home revoked the person: %+v, want 401", a)
	}

	// signed in again later; then an admin resets the password (the copy says it must
	// change)
	g.now = g.now.Add(revokedKeep + time.Second)
	_, id = g.signIn(t, "amal", "pw-amal")
	if a := g.do(http.MethodGet, "/api/videos", id); a.code != http.StatusOK {
		t.Fatalf("signed in again: %+v", a)
	}
	was := copyUsers["u1"]
	t.Cleanup(func() { copyUsers["u1"] = was })
	reset := was
	reset.MustChangePassword = true
	copyUsers["u1"] = reset
	if a := g.do(http.MethodGet, "/api/videos", id); a.code != http.StatusUnauthorized {
		t.Fatalf("a node-local session of a person who must change their password: %+v, want 401", a)
	}
}

// Spraying names past the counter's room does not unlock a locked one.
func TestSprayingNamesDoesNotUnlockOne(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	r := &Resolver{Now: func() time.Time { return now }}
	for i := 0; i < lockoutThreshold; i++ {
		r.failed("amal")
	}
	if !r.locked("amal") {
		t.Fatal("not locked after the threshold")
	}
	for i := 0; i < maxEntries+500; i++ {
		r.failed(fmt.Sprintf("spray-%d", i))
	}
	if !r.locked("amal") {
		t.Fatal("spraying names unlocked a locked one")
	}
	if len(r.fails) > maxEntries {
		t.Fatalf("the counter grew past its room: %d", len(r.fails))
	}
	// past the window the lock ends as always
	now = now.Add(lockoutWindow + time.Second)
	if r.locked("amal") {
		t.Fatal("still locked past the window")
	}
}
