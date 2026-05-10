package httpsec

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHeaders_StampsAllConfiguredHeaders(t *testing.T) {
	cfg := DefaultHeaders()
	cfg.HSTS = HSTSOneYear
	mw := Headers(cfg)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	cases := map[string]string{
		"Strict-Transport-Security":  HSTSOneYear,
		"X-Content-Type-Options":     "nosniff",
		"Referrer-Policy":            "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy": "same-origin",
	}
	for k, want := range cases {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("CSP header should be set")
	}
}

func TestHeaders_HSTSOmittedWhenEmpty(t *testing.T) {
	cfg := DefaultHeaders() // HSTS empty
	mw := Headers(cfg)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Errorf("HSTS should be omitted when config empty, got %q",
			rec.Header().Get("Strict-Transport-Security"))
	}
}

func TestCORS_AllowedOriginGetsHeaders(t *testing.T) {
	cfg := DefaultCORS()
	cfg.AllowedOrigins = []string{"https://app.maktaba.local"}
	mw := CORS(cfg)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://app.maktaba.local")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.maktaba.local" {
		t.Errorf("Allow-Origin = %q, want app.maktaba.local", got)
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("Allow-Credentials should be true")
	}
}

func TestCORS_UnknownOriginNoHeaders(t *testing.T) {
	cfg := DefaultCORS()
	cfg.AllowedOrigins = []string{"https://app.maktaba.local"}
	mw := CORS(cfg)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin should be empty for unknown origin, got %q", got)
	}
}

func TestCORS_PreflightAllowedOrigin(t *testing.T) {
	cfg := DefaultCORS()
	cfg.AllowedOrigins = []string{"https://x"}
	cfg.MaxAge = 600
	mw := CORS(cfg)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler should not run for preflight")
	}))
	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "https://x")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("preflight should advertise allowed methods")
	}
	if rec.Header().Get("Access-Control-Max-Age") != "600" {
		t.Errorf("Max-Age = %q, want 600", rec.Header().Get("Access-Control-Max-Age"))
	}
}

func TestCORS_PreflightUnknownOriginStill204NoHeaders(t *testing.T) {
	cfg := DefaultCORS()
	cfg.AllowedOrigins = []string{"https://x"}
	mw := CORS(cfg)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler should not run for preflight")
	}))
	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "https://evil")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("unknown origin preflight should set no CORS headers")
	}
}

func TestSecureCookie_ProductionAttributes(t *testing.T) {
	t.Setenv("MAKTABA_DEV", "")
	c := SecureCookie("mkt_session", "abc", 3600, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !c.Secure {
		t.Error("Secure should be true in non-dev")
	}
	if !c.HttpOnly {
		t.Error("HttpOnly should be true when requested")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}
	if c.MaxAge != 3600 {
		t.Errorf("MaxAge = %d, want 3600", c.MaxAge)
	}
}

func TestSecureCookie_DevDropsSecure(t *testing.T) {
	t.Setenv("MAKTABA_DEV", "1")
	c := SecureCookie("mkt_session", "abc", 3600, true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if c.Secure {
		t.Error("Secure should be dropped in dev mode")
	}
}

func TestParseAllowedOrigins(t *testing.T) {
	got := ParseAllowedOrigins("https://a, https://b ,, https://c ")
	want := []string{"https://a", "https://b", "https://c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if got := ParseAllowedOrigins(""); got != nil {
		t.Errorf("empty input should be nil, got %v", got)
	}
}
