package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
)

type fakeACL struct {
	libs []string
	err  error
}

func (f fakeACL) LibrariesFor(_ context.Context, _ string) ([]string, error) {
	return f.libs, f.err
}

func testKeys(t *testing.T) *keys.Set {
	t.Helper()
	s := keys.NewSet(time.Hour)
	k, err := keys.Generate(keys.MinBits)
	if err != nil {
		t.Fatal(err)
	}
	s.Replace(k)
	return s
}

// R3.1: a non-admin user with two library_acl rows gets those ids
// snapshotted into the minted access token's `lib` claim.
func TestLibrariesFor_NonAdminSnapshotsACL(t *testing.T) {
	h := &Handler{ACL: fakeACL{libs: []string{"lib-a", "lib-b"}}}
	libs, err := h.librariesFor(context.Background(), &users.User{ID: "u1", IsAdmin: false})
	if err != nil {
		t.Fatalf("librariesFor: %v", err)
	}
	if len(libs) != 2 || libs[0] != "lib-a" || libs[1] != "lib-b" {
		t.Fatalf("libs = %v, want [lib-a lib-b]", libs)
	}

	set := testKeys(t)
	tok, err := jwt.Sign(set, accessClaims(&users.User{ID: "u1"}, libs, time.Now().UTC(), 15*time.Minute))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	c, err := jwt.Verify(set, tok, "api")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(c.Lib) != 2 || c.Lib[0] != "lib-a" || c.Lib[1] != "lib-b" {
		t.Fatalf("decoded lib = %v, want [lib-a lib-b]", c.Lib)
	}
	if c.Usr != "u1" {
		t.Fatalf("decoded usr = %q, want u1", c.Usr)
	}
}

// Admins read everything via AccessAllLibraries; the ACL is not even
// consulted (lib stays empty).
func TestLibrariesFor_AdminSkipsACL(t *testing.T) {
	h := &Handler{ACL: fakeACL{err: errors.New("must not be called")}}
	libs, err := h.librariesFor(context.Background(), &users.User{ID: "admin", IsAdmin: true})
	if err != nil {
		t.Fatalf("admin path should not error: %v", err)
	}
	if len(libs) != 0 {
		t.Fatalf("admin libs = %v, want empty", libs)
	}
}

// An ACL backend failure must propagate so the handler can 500 rather
// than mint a token with a silently-empty lib[] (which would lock the
// user out of streaming).
func TestLibrariesFor_ACLErrorPropagates(t *testing.T) {
	want := errors.New("db down")
	h := &Handler{ACL: fakeACL{err: want}}
	_, err := h.librariesFor(context.Background(), &users.User{ID: "u1"})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// R3.3: GET /api/auth/me projects the request principal. The route is
// gated globally by RequireAuthExcept (not in the public allowlist),
// but Me also defends in depth — no principal → 401.
func TestMe_ProjectsPrincipal(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{
		UserID:    "user-7",
		IsAdmin:   true,
		Libraries: []string{"lib-a", "lib-b"},
	}))
	rec := httptest.NewRecorder()
	h.Me(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		UserID    string   `json:"user_id"`
		IsAdmin   bool     `json:"is_admin"`
		Libraries []string `json:"libraries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if got.UserID != "user-7" || !got.IsAdmin {
		t.Fatalf("projection = %+v", got)
	}
	if len(got.Libraries) != 2 || got.Libraries[0] != "lib-a" || got.Libraries[1] != "lib-b" {
		t.Fatalf("libraries = %v, want [lib-a lib-b]", got.Libraries)
	}
}

func TestMe_NoPrincipalIs401(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.Me(rec, httptest.NewRequest(http.MethodGet, "/api/auth/me", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous /api/auth/me: status = %d, want 401", rec.Code)
	}
}

func TestMe_NilLibrariesProjectsEmptyArray(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{
		UserID: "u1",
	}))
	rec := httptest.NewRecorder()
	h.Me(rec, req)
	// libraries must serialize as [] not null so clients can iterate.
	if body := rec.Body.String(); !contains(body, `"libraries":[]`) {
		t.Fatalf("body = %s, want libraries:[]", body)
	}
}

func TestIsNativeClient(t *testing.T) {
	cases := []struct {
		name string
		hdr  http.Header
		want bool
	}{
		{"web (no headers)", http.Header{}, false},
		{"client header", http.Header{HeaderClient: []string{"native"}}, true},
		{"client header mixed case", http.Header{HeaderClient: []string{"Native"}}, true},
		{"authorization header present", http.Header{"Authorization": []string{"Bearer xxx"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
			r.Header = tc.hdr
			if got := isNativeClient(r); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRemoteIP_StripsPort(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "10.1.2.3:54321"
	if got := remoteIP(r); got != "10.1.2.3" {
		t.Errorf("got %q want 10.1.2.3", got)
	}
}

func TestRemoteIP_NoPortFallsBack(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "10.1.2.3"
	if got := remoteIP(r); got != "10.1.2.3" {
		t.Errorf("got %q", got)
	}
}

func TestEnvAccessTTL_Default(t *testing.T) {
	if got := EnvAccessTTL(func(_ string) string { return "" }); got != DefaultAccessTTL {
		t.Errorf("got %v want %v", got, DefaultAccessTTL)
	}
}

func TestEnvAccessTTL_Custom(t *testing.T) {
	got := EnvAccessTTL(func(k string) string {
		if k == "MAKTABA_AUTH_ACCESS_TTL_SEC" {
			return "60"
		}
		return ""
	})
	if got != 60*time.Second {
		t.Errorf("got %v", got)
	}
}

func TestEnvAccessTTL_BadInputFallsBack(t *testing.T) {
	got := EnvAccessTTL(func(_ string) string { return "not-an-int" })
	if got != DefaultAccessTTL {
		t.Errorf("got %v", got)
	}
}

func TestClearCookie_SessionIsHttpOnly(t *testing.T) {
	w := httptest.NewRecorder()
	clearCookie(w, CookieSession, false)
	hdr := w.Header().Get("Set-Cookie")
	if hdr == "" {
		t.Fatal("expected Set-Cookie")
	}
	// httpOnly is required for the session cookie even on clear.
	if !contains(hdr, "HttpOnly") {
		t.Errorf("expected HttpOnly: %q", hdr)
	}
}

func TestClearCookie_CSRFIsNotHttpOnly(t *testing.T) {
	w := httptest.NewRecorder()
	clearCookie(w, CookieCSRF, false)
	hdr := w.Header().Get("Set-Cookie")
	if hdr == "" {
		t.Fatal("expected Set-Cookie")
	}
	if contains(hdr, "HttpOnly") {
		t.Errorf("CSRF cookie should NOT be HttpOnly: %q", hdr)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
