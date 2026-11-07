package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// T10 (Epic 50 story 50.32 §4): the refresh cookie is __Host-: host-only (no Domain, even if
// one is passed), Path=/, Secure, HttpOnly, SameSite=Lax; its clearing matches it.
func TestTheRefreshCookieIsHostOnly(t *testing.T) {
	set := httptest.NewRecorder()
	setRefreshCookie(set, "raw-token", ".maktaba.tv", true, time.Now().Add(time.Hour))
	clear := httptest.NewRecorder()
	clearRefreshCookie(clear, ".maktaba.tv", true)
	for name, rec := range map[string]*httptest.ResponseRecorder{"set": set, "clear": clear} {
		raw := rec.Header().Get("Set-Cookie")
		cs := rec.Result().Cookies()
		if len(cs) != 1 {
			t.Fatalf("%s: %q", name, raw)
		}
		c := cs[0]
		if c.Name != "__Host-maktaba_refresh" || c.Path != "/" || c.Domain != "" || !c.Secure || !c.HttpOnly ||
			c.SameSite != http.SameSiteLaxMode || strings.Contains(strings.ToLower(raw), "domain=") {
			t.Errorf("%s: %q", name, raw)
		}
	}
	if !strings.Contains(set.Header().Get("Set-Cookie"), "raw-token") || clear.Result().Cookies()[0].MaxAge >= 0 {
		t.Error("the set carries no token, or the clear does not expire it")
	}
}
