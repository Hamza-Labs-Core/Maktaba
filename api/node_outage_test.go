package main

// A full node that loses its home, through its real front (the placement table, the
// principal, the auth gate, CSRF per host, the VOD guard): reads and playback go on for
// a signed-in person, from this machine's copy of the home's database, marked
// X-Maktaba-Home: unreachable; a reload keeps them signed in; a sign-in is answered from
// the copy; changes through the home are refused at once.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homelink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homeproxy"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/nodeauth"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/vodfallback"
)

const outageCSRF = "csrf-token-1"

type outageHome struct {
	mu   sync.Mutex
	down bool
	hits int
	srv  *httptest.Server
}

func (f *outageHome) isDown() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.down }

func (f *outageHome) Transport() http.RoundTripper { return http.DefaultTransport }
func (f *outageHome) HomeURL() string              { return f.srv.URL }
func (f *outageHome) Introspect(context.Context, delegation.IntrospectRequest) (*delegation.IntrospectAnswer, error) {
	if f.isDown() {
		return nil, homelink.ErrUnreachable
	}
	sum := sha256.Sum256([]byte(outageCSRF))
	return &delegation.IntrospectAnswer{Source: "cookie", CSRFSHA256: hex.EncodeToString(sum[:]),
		ExpiresAt: time.Now().Add(24 * time.Hour),
		Principal: delegation.Principal{UserID: "u1", Username: "amal", Libraries: []string{}}}, nil
}
func (f *outageHome) Exchange(context.Context, delegation.ExchangeRequest) (*delegation.ExchangeAnswer, error) {
	return nil, homelink.ErrUnauthorized
}
func (f *outageHome) JWKS(context.Context) ([]byte, error) { return []byte(`{"keys":[]}`), nil }
func (f *outageHome) Self(context.Context) (json.RawMessage, error) {
	if f.isDown() {
		return nil, errors.New("down")
	}
	return json.RawMessage(`{}`), nil
}
func (f *outageHome) Subscribe(func(homelink.Event)) {}
func (f *outageHome) HasKey() bool                   { return true }
func (f *outageHome) SignOut(context.Context, delegation.SignOutRequest) (*delegation.SignOutAnswer, error) {
	if f.isDown() {
		return nil, homelink.ErrUnreachable
	}
	return &delegation.SignOutAnswer{}, nil
}

// outageCopy is this machine's copy: the session the home answered for, one user.
type outageCopy struct{}

var outageUser = delegation.Principal{UserID: "u1", Username: "amal", Libraries: []string{}}

func (outageCopy) Introspect(_ context.Context, req delegation.IntrospectRequest) (*delegation.IntrospectAnswer, error) {
	if req.Session != outageSession {
		return nil, nodeauth.ErrNotLive
	}
	sum := sha256.Sum256([]byte(outageCSRF))
	return &delegation.IntrospectAnswer{Source: "cookie", CSRFSHA256: hex.EncodeToString(sum[:]),
		ExpiresAt: time.Now().Add(24 * time.Hour), Principal: outageUser}, nil
}
func (outageCopy) Principal(_ context.Context, id string) (*delegation.Principal, error) {
	if id != outageUser.UserID {
		return nil, nodeauth.ErrNotLive
	}
	p := outageUser
	return &p, nil
}
func (outageCopy) SignIn(_ context.Context, name, pw string) (*delegation.Principal, error) {
	if name != "amal" || pw != "pw" {
		return nil, nodeauth.ErrNotLive
	}
	p := outageUser
	return &p, nil
}
func (outageCopy) Synced(context.Context) (time.Time, error) { return time.Now(), nil }

const outageSession = "6f1d2c3b-0000-4000-8000-000000000002"

func TestNodeOutageKeepsReadsAndPlayback(t *testing.T) {
	home := &outageHome{}
	home.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		home.mu.Lock()
		home.hits++
		home.mu.Unlock()
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(home.srv.Close)
	now := time.Now()
	resolver := &nodeauth.Resolver{Link: home, Now: func() time.Time { return now }, Local: outageCopy{},
		Sessions: &nodeauth.LocalSessions{Now: func() time.Time { return now }}}

	r := chi.NewRouter()
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	r.Get("/api/videos", ok)                         // any
	r.Post("/api/stream/sessions", ok)               // local: playback starts
	r.Delete("/api/stream/sessions/{id}", ok)        // local: and stops
	r.Post("/api/stream/sessions/{id}/progress", ok) // home: viewing data
	rt := &nodeRuntime{resolver: resolver, vod: &vodfallback.Fallback{},
		proxy: homeproxy.NewWithFallback(home, nil, resolver.Me)}
	h := rt.handler(&authState{}, r, r)

	do := func(method, path string, signedIn bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		if signedIn {
			req.AddCookie(&http.Cookie{Name: nodeauth.CookieSession, Value: outageSession})
			req.AddCookie(&http.Cookie{Name: nodeauth.CookieCSRF, Value: outageCSRF})
			req.Header.Set(nodeauth.CSRFHeader, outageCSRF)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := do(http.MethodGet, "/api/videos", true); rec.Code != http.StatusOK {
		t.Fatalf("home up: %d", rec.Code)
	}

	home.mu.Lock()
	home.down = true
	home.mu.Unlock()
	now = now.Add(nodeauth.PositiveTTL + nodeauth.DefaultGrace + 2*time.Second)
	if rec := do(http.MethodGet, "/api/videos", true); rec.Code != http.StatusOK || rec.Header().Get(nodeauth.HomeStateHeader) != "unreachable" {
		t.Fatalf("a read in the outage: %d %q", rec.Code, rec.Header().Get(nodeauth.HomeStateHeader))
	}
	now = now.Add(nodeauth.DefaultGrace + time.Second) // past the grace since the first miss
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/stream/sessions"},
		{http.MethodDelete, "/api/stream/sessions/abc"},
	} {
		if rec := do(c.method, c.path, true); rec.Code != http.StatusOK {
			t.Fatalf("%s %s in the outage (CSRF checked against the copy's answer): %d %s",
				c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	if rec := do(http.MethodGet, nodeauth.MePath, true); rec.Code != http.StatusOK {
		t.Fatalf("a reload's session probe: %d %s", rec.Code, rec.Body.String())
	}
	hits := home.hits
	// a sign-in is checked against the copy: a node-local session
	req := httptest.NewRequest(http.MethodPost, nodeauth.LoginPath, strings.NewReader(`{"username":"amal","password":"pw"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Set-Cookie"), nodeauth.LocalSessionPrefix) {
		t.Fatalf("a sign-in in the outage: %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range []struct {
		method, path string
		signedIn     bool
	}{
		{http.MethodPost, "/api/auth/logout", true}, // a home session's: the home's
		{http.MethodPost, "/api/stream/sessions/abc/progress", true},
	} {
		rec := do(c.method, c.path, c.signedIn)
		var p struct{ Code string }
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		if rec.Code != http.StatusServiceUnavailable || p.Code != "home_unreachable" {
			t.Fatalf("%s %s in the outage: %d %s, want 503 home_unreachable", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	if home.hits != hits {
		t.Fatalf("the home was tried %d times past the grace", home.hits-hits)
	}
}
