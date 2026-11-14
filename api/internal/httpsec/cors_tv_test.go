package httpsec

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Epic 58 story 58.09 T05: GET /api/discovery/home answers any origin with `*` and no
// credentials (the launcher reads it from its packaged file:// page), preflight included;
// other routes keep the allow-list.
func TestCORS_PublicDiscoveryAnswersAnyOrigin(t *testing.T) {
	h := CORS(DefaultCORS())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, origin := range []string{"file://", "null", "https://anything.example"} {
		pre := httptest.NewRequest(http.MethodOptions, "/api/discovery/home", nil)
		pre.Header.Set("Origin", origin)
		pre.Header.Set("Access-Control-Request-Method", "GET")
		pre.Header.Set("Access-Control-Request-Headers", "x-maktaba-client")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, pre)
		if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("%s preflight: %d, Allow-Origin %q", origin, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
		}
		if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("%s preflight allows credentials", origin)
		}
		if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
			t.Fatalf("%s preflight names no headers", origin)
		}

		get := httptest.NewRequest(http.MethodGet, "/api/discovery/home", nil)
		get.Header.Set("Origin", origin)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, get)
		if rec.Code != http.StatusOK || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("%s GET: %d, Allow-Origin %q", origin, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
		}
		if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("%s GET allows credentials", origin)
		}
	}
	// another route: a web origin still gets nothing
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/libraries", nil)
	req.Header.Set("Origin", "https://anything.example")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("/api/libraries for a web origin: Allow-Origin %q", got)
	}
}

// A packaged TV app's origin (the --bundle-ui build) is answered with its origin, never
// with credentials: the browser then sends no cookie on such a call (a credentialed
// preflight needs Allow-Credentials) and the app uses its bearer token alone.
func TestCORS_PackagedAppOriginWithoutCredentials(t *testing.T) {
	h := CORS(DefaultCORS())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, origin := range []string{"file://", "null", "app://tv.maktaba"} {
		pre := httptest.NewRequest(http.MethodOptions, "/api/libraries", nil)
		pre.Header.Set("Origin", origin)
		pre.Header.Set("Access-Control-Request-Method", "GET")
		pre.Header.Set("Access-Control-Request-Headers", "authorization, x-maktaba-client")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, pre)
		hd := rec.Header()
		if hd.Get("Access-Control-Allow-Origin") != origin || hd.Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("%s preflight: Allow-Origin %q, Allow-Credentials %q", origin,
				hd.Get("Access-Control-Allow-Origin"), hd.Get("Access-Control-Allow-Credentials"))
		}
		if got := hd.Get("Access-Control-Allow-Headers"); got != packagedHeaders {
			t.Fatalf("%s preflight headers = %q (no CSRF header: a cookie session cannot be used)", origin, got)
		}
	}
	// the packaged origin with no bearer (a sandboxed web page says null too): no answer, so
	// nothing that needs no sign-in can be read cross-origin
	for _, origin := range []string{"file://", "null"} {
		pre := httptest.NewRequest(http.MethodOptions, "/api/libraries", nil)
		pre.Header.Set("Origin", origin)
		pre.Header.Set("Access-Control-Request-Method", "GET")
		pre.Header.Set("Access-Control-Request-Headers", "content-type")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, pre)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s preflight without Authorization: Allow-Origin %q", origin, got)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
		req.Header.Set("Origin", origin)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s GET without a bearer: Allow-Origin %q", origin, got)
		}
		// a bearer header alone proves nothing (an invalid token is no sign-in)
		req.Header.Set("Authorization", "Bearer garbage")
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s GET with an unverified bearer: Allow-Origin %q", origin, got)
		}
		// verified by the API (MarkVerified), or a 401: answered
		verified := CORS(DefaultCORS())(MarkVerified(func(*http.Request) bool { return true })(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{}")) })))
		rec = httptest.NewRecorder()
		verified.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("%s GET with a verified bearer: Allow-Origin %q", origin, got)
		}
		unauth := CORS(DefaultCORS())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		rec = httptest.NewRecorder()
		unauth.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Fatalf("%s 401: Allow-Origin %q, want it readable (the TV renews on it)", origin, got)
		}
	}
	// before it holds a token: whether it is signed in, a renewal and a code's redemption are
	// answered; starting a code is not (a page claiming null must not read a fresh code)
	for path, want := range map[string]string{
		"/api/auth/me": "null", "/api/auth/refresh": "null", "/api/pairing/device/token": "null",
		"/api/pairing/device": "",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Origin", "null")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Fatalf("null %s without a bearer: Allow-Origin %q, want %q", path, got, want)
		}
		if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("null %s: credentials allowed", path)
		}
	}
	// a web origin, or one that only looks packaged, is not one
	for _, origin := range []string{"https://evil.example", "file://evil.example", "nullx"} {
		req := httptest.NewRequest(http.MethodGet, "/api/libraries", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s: Allow-Origin %q", origin, got)
		}
	}
	// switched off: nothing
	cfg := DefaultCORS()
	cfg.PackagedApps = false
	off := CORS(cfg)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/api/libraries", nil)
	req.Header.Set("Origin", "file://")
	rec := httptest.NewRecorder()
	off.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("PackagedApps off: Allow-Origin %q", got)
	}
}

// An origin on the operator's allow-list keeps its own (credentialed) answer.
func TestCORS_AllowListedOriginKeepsCredentials(t *testing.T) {
	cfg := DefaultCORS()
	cfg.AllowedOrigins = []string{"https://app.maktaba.local"}
	h := CORS(cfg)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/api/discovery/home", nil)
	req.Header.Set("Origin", "https://app.maktaba.local")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.maktaba.local" ||
		rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("listed origin: %v", rec.Header())
	}
}
