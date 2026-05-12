package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsNativeClient(t *testing.T) {
	cases := []struct {
		name    string
		hdr     http.Header
		want    bool
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
