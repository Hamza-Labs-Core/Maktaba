package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
)

// Epic 58 story 58.09: the CORS answer a packaged TV app's origin gets (the Samsung and
// LG launcher's --bundle-ui build) changes no rule. Through the real security stack, a
// request from `file://` or `null` is still refused without a token (401), a viewer's
// token still cannot reach an admin route (403), an admin's can, and no answer to such an
// origin ever allows credentials (so a browser never sends, or shows, a cookie session to
// it). The public discovery route answers `*`.
func TestPackagedTVOrigin_RolesUnchanged(t *testing.T) {
	priv, pub := mustPEMPair(t)
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", priv)
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", pub)
	t.Setenv("MAKTABA_ADMIN_TOKEN", "")
	t.Setenv("MAKTABA_CORS_ALLOWED_ORIGINS", "")
	t.Setenv("MAKTABA_HSTS", "")
	st, err := initAuth(noopLogger())
	if err != nil {
		t.Fatalf("initAuth: %v", err)
	}
	mux := http.NewServeMux()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("/api/libraries", ok)
	mux.Handle("/api/users", middleware.RequireAdmin(ok))
	mux.Handle("/api/discovery/home", ok)
	// a route that needs no sign-in (on the public allow-list): starting a pairing code
	mux.Handle("/api/pairing/device", ok)
	stack := st.applySecurity(mux, nil, nil, nil)

	k, err := keys.FromPEM(priv, pub)
	if err != nil {
		t.Fatal(err)
	}
	set := keys.NewSet(time.Hour)
	set.Replace(k)
	sign := func(admin bool) string {
		tok, err := jwt.Sign(set, jwt.Claims{Iss: "maktaba", Aud: "api", Sub: "u1", Usr: "u1", IsAdmin: admin})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	viewer, admin := sign(false), sign(true)

	call := func(origin, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Origin", origin)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		stack.ServeHTTP(rec, req)
		return rec
	}
	for _, origin := range []string{"file://", "null"} {
		cases := []struct {
			path, token string
			want        int
		}{
			{"/api/libraries", "", http.StatusUnauthorized},
			{"/api/libraries", viewer, http.StatusOK},
			{"/api/users", "", http.StatusUnauthorized},
			{"/api/users", viewer, http.StatusForbidden},
			{"/api/users", admin, http.StatusOK},
		}
		for _, c := range cases {
			rec := call(origin, c.path, c.token)
			if rec.Code != c.want {
				t.Errorf("%s %s (token %v): %d, want %d", origin, c.path, c.token != "", rec.Code, c.want)
			}
			// answered for a verified bearer, and on a 401 (the TV renews on it)
			want := origin
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != want {
				t.Errorf("%s %s (token %v): Allow-Origin %q, want %q", origin, c.path, c.token != "", got, want)
			}
			if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
				t.Errorf("%s %s: Allow-Credentials %q, want none", origin, c.path, got)
			}
		}
		rec := call(origin, "/api/discovery/home", "")
		if rec.Code != http.StatusOK || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%s discovery: %d, Allow-Origin %q", origin, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
		}
	}
	// a page claiming null with a made-up bearer reads no route that needs no sign-in: an
	// invalid token is no sign-in, and the packaged origin is answered only once verified
	for _, tok := range []string{"", "garbage", "pat_garbage"} {
		req := httptest.NewRequest(http.MethodPost, "/api/pairing/device", nil)
		req.Header.Set("Origin", "null")
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		stack.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("pairing start (bearer %q): %d, want the route to run", tok, rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("pairing start with bearer %q: Allow-Origin %q, want none", tok, got)
		}
	}
	// a web page's origin still gets no CORS answer on the API
	if got := call("https://evil.example", "/api/libraries", viewer).Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("web origin: Allow-Origin %q", got)
	}
}
