package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// Story 23.6 AC-1: /api/auth/login is capped at 10/min/IP — far below
// the generic 6000/min limiter. The 11th request in the burst window
// from the same IP must be a structured 429 + Retry-After.
func TestAuthRouteRateLimit_LoginCappedAt10(t *testing.T) {
	mw := AuthRouteRateLimit(DefaultAuthRouteLimits())
	h := mw(okHandler())

	var lastCode int
	for i := 0; i < 11; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		req.RemoteAddr = "203.0.113.7:5555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		lastCode = rec.Code
		if i < 10 && rec.Code != http.StatusOK {
			t.Fatalf("request %d: status=%d want 200 (within cap)", i+1, rec.Code)
		}
		if i == 10 {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("request 11: status=%d want 429", rec.Code)
			}
			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("429 must carry Retry-After")
			}
		}
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("final status=%d want 429", lastCode)
	}
}

// refresh gets a higher (60/min) ceiling than login.
func TestAuthRouteRateLimit_RefreshHigherCeiling(t *testing.T) {
	mw := AuthRouteRateLimit(DefaultAuthRouteLimits())
	h := mw(okHandler())
	// 11 refreshes (would 429 on the login cap) must all pass.
	for i := 0; i < 11; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
		req.RemoteAddr = "203.0.113.8:5555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("refresh %d: status=%d want 200", i+1, rec.Code)
		}
	}
}

// A path not in the table is untouched — the generic limiter still
// bounds it, this layer is a no-op.
func TestAuthRouteRateLimit_UntabledPathPassThrough(t *testing.T) {
	mw := AuthRouteRateLimit(DefaultAuthRouteLimits())
	h := mw(okHandler())
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/libraries", nil)
		req.RemoteAddr = "203.0.113.9:5555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("untabled request %d: status=%d want 200", i+1, rec.Code)
		}
	}
}

// Per-IP isolation: one IP exhausting the login cap must not lock out
// a different IP.
func TestAuthRouteRateLimit_PerIPIsolation(t *testing.T) {
	mw := AuthRouteRateLimit(DefaultAuthRouteLimits())
	h := mw(okHandler())
	for i := 0; i < 11; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		req.RemoteAddr = "198.51.100.1:1111"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	// Fresh IP — first request must still succeed.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "198.51.100.2:2222"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fresh IP status=%d want 200 (per-IP isolation)", rec.Code)
	}
}

func TestDefaultAuthRouteLimits_Table(t *testing.T) {
	got := map[string]int{}
	for _, r := range DefaultAuthRouteLimits() {
		got[r.Path] = r.PerMin
	}
	want := map[string]int{
		"/api/auth/login":      10,
		"/api/auth/refresh":    60,
		"/api/auth/logout":     30,
		"/api/auth/logout-all": 30,
	}
	for p, w := range want {
		if got[p] != w {
			t.Errorf("table[%q] = %d, want %d", p, got[p], w)
		}
	}
}
