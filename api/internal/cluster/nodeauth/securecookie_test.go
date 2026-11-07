package nodeauth

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestNodeLocalCookiesSecureOverTLS: a sign-in on the copy over HTTPS sets the session
// and CSRF cookies Secure though SecureCookies is off (MAKTABA_HSTS unset); over plain
// HTTP, as on a LAN, they are not, so the browser can send them back (CodeQL
// go/cookie-secure-not-set, alerts 39 and 40).
func TestNodeLocalCookiesSecureOverTLS(t *testing.T) {
	g := newOfflineRig(t, true)
	g.goDown(time.Minute)
	g.pastGrace()
	if g.r.SecureCookies {
		t.Fatal("the rig is meant to run with SecureCookies off")
	}
	for _, overTLS := range []bool{true, false} {
		req := httptest.NewRequest(http.MethodPost, LoginPath, strings.NewReader(`{"username":"amal","password":"pw-amal"}`))
		if overTLS {
			req.TLS = &tls.ConnectionState{}
		}
		rec := httptest.NewRecorder()
		g.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("tls=%v: sign-in %d %s", overTLS, rec.Code, rec.Body.String())
		}
		cookies := rec.Result().Cookies()
		if len(cookies) < 2 {
			t.Fatalf("tls=%v: cookies %v", overTLS, cookies)
		}
		for _, c := range cookies {
			if (c.Name == CookieSession || c.Name == CookieCSRF) && c.Secure != overTLS {
				t.Errorf("tls=%v: cookie %s Secure=%v", overTLS, c.Name, c.Secure)
			}
		}
	}
}
