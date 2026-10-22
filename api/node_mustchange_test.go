package main

// A web session whose password an admin reset (users.must_change_password, PR #131)
// is held to the change-password form on a full node too: the node's principal
// carries the home's must-change answer and the node runs the same auth gate as the
// home (middleware.RequireAuthExcept).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homelink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/nodeauth"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/vodfallback"
)

// fakeHome answers the node's introspection like the home's clusterauth.Home.
type fakeHome struct {
	mu         sync.Mutex
	mustChange bool
	calls      int
}

func (f *fakeHome) set(must bool) { f.mu.Lock(); f.mustChange = must; f.mu.Unlock() }
func (f *fakeHome) count() int    { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

func (f *fakeHome) Transport() http.RoundTripper { return http.DefaultTransport }
func (f *fakeHome) HomeURL() string              { return "http://127.0.0.1:1" }
func (f *fakeHome) Introspect(_ context.Context, req delegation.IntrospectRequest) (*delegation.IntrospectAnswer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	src := "cookie"
	if req.PAT != "" {
		src = "pat"
	}
	return &delegation.IntrospectAnswer{Source: src, Principal: delegation.Principal{UserID: "u1", Username: "amal",
		Libraries: []string{}, MustChangePassword: f.mustChange}}, nil
}
func (f *fakeHome) Exchange(context.Context, delegation.ExchangeRequest) (*delegation.ExchangeAnswer, error) {
	return nil, homelink.ErrUnauthorized
}
func (f *fakeHome) JWKS(context.Context) ([]byte, error)          { return []byte(`{"keys":[]}`), nil }
func (f *fakeHome) Self(context.Context) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }
func (f *fakeHome) Subscribe(func(homelink.Event))                {}
func (f *fakeHome) HasKey() bool                                  { return true }
func (f *fakeHome) SignOut(context.Context, delegation.SignOutRequest) (*delegation.SignOutAnswer, error) {
	return &delegation.SignOutAnswer{}, nil
}

func TestNodeHoldsMustChangeSessionToPasswordForm(t *testing.T) {
	home := &fakeHome{mustChange: true}
	r := chi.NewRouter()
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	r.Get("/api/videos", ok)           // any: the node serves it
	r.Get("/api/system/version", ok)   // local, on the allowlist
	r.Post("/api/stream/sessions", ok) // local VOD
	r.Get("/api/me", ok)               // home: proxied
	proxied := 0
	rt := &nodeRuntime{
		resolver: &nodeauth.Resolver{Link: home},
		vod:      &vodfallback.Fallback{},
		proxy: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			proxied++
			w.WriteHeader(http.StatusTeapot)
		}),
	}
	h := rt.handler(&authState{}, r, r)

	do := func(method, path string, cred *http.Cookie, bearer string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		if cred != nil {
			req.AddCookie(cred)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var body struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body.Type
	}
	sess := &http.Cookie{Name: nodeauth.CookieSession, Value: "6f1d2c3b-0000-4000-8000-000000000001"}

	// must change: the node's own routes are refused...
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/videos"}, {http.MethodPost, "/api/stream/sessions"},
	} {
		if code, typ := do(c.method, c.path, sess, ""); code != http.StatusForbidden || typ != middleware.TypePasswordChangeRequired {
			t.Fatalf("%s %s with a must-change session on the node: %d %q, want 403 password-change-required",
				c.method, c.path, code, typ)
		}
	}
	// ...but the allowlist holds on the node, and home routes go to the home (which
	// applies the same gate itself)
	if code, _ := do(http.MethodGet, "/api/system/version", sess, ""); code != http.StatusOK {
		t.Fatalf("allowlisted local route: %d", code)
	}
	if code, _ := do(http.MethodGet, "/api/me", sess, ""); code != http.StatusTeapot || proxied != 1 {
		t.Fatalf("GET /api/me: %d (proxied %d), want it proxied to the home", code, proxied)
	}
	// a must-change answer is not cached: every request asked the home
	before := home.count()
	if before != 3 { // two refused, one allowlisted; /api/me went to the home
		t.Fatalf("introspections = %d, want one per signed-in request while the password must change", before)
	}

	// the password is changed on the home; even without the revocation push the very
	// next request on the node is let through
	home.set(false)
	if code, _ := do(http.MethodGet, "/api/videos", sess, ""); code != http.StatusOK {
		t.Fatalf("after the change: %d, want 200", code)
	}
	n := home.count()
	if code, _ := do(http.MethodGet, "/api/videos", sess, ""); code != http.StatusOK || home.count() != n {
		t.Fatalf("an ordinary answer is cached as before: %d, introspections %d -> %d", code, n, home.count())
	}

	// an admin resets it again: the home pushes the user's revocation (story 35.9 AC4),
	// so the node does not keep a stale "ok"
	home.set(true)
	rt.resolver.OnEvent(homelink.Event{Kind: "revoked", Revoked: &delegation.Revoked{UserID: "u1", Reason: "password_reset"}})
	if code, typ := do(http.MethodGet, "/api/videos", sess, ""); code != http.StatusForbidden || typ != middleware.TypePasswordChangeRequired {
		t.Fatalf("after a second reset: %d %q, want 403", code, typ)
	}

	// the gate is a web-session rule: a personal access token is not held to it (as on
	// the home)
	if code, _ := do(http.MethodGet, "/api/videos", nil, "pat_example"); code != http.StatusOK {
		t.Fatalf("PAT of a must-change user: %d, want 200", code)
	}
}
