package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func ok(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("served")) }

// Epic 50 story 50.32 §1: a listener answers its own names; another Host gets the body of an
// unknown route; the node's probes answer on any Host; a wrong Host is logged.
func TestOnlyHosts(t *testing.T) {
	var logs bytes.Buffer
	h := OnlyHosts(slog.New(slog.NewTextHandler(&logs, nil)), "admin", []string{"admin.maktaba.tv"})(http.HandlerFunc(ok))
	unknown := httptest.NewRecorder()
	http.NotFound(unknown, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, c := range []struct {
		host, path string
		served     bool
	}{
		{"admin.maktaba.tv", "/v1/admin/accounts", true},
		{"ADMIN.maktaba.tv:8091", "/v1/admin/accounts", true},
		{"admin.maktaba.tv.", "/v1/admin/accounts", true},
		{"api.maktaba.tv", "/v1/admin/accounts", false},
		{"admin.maktaba.tv.evil.test", "/v1/admin/accounts", false},
		{"127.0.0.1:8091", "/v1/admin/accounts", false},
		{"127.0.0.1:8091", "/healthz", true},
		{"127.0.0.1:8091", "/readyz", true},
		{"127.0.0.1:8090", "/metrics", true},
	} {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		req.Host = c.host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if served := rec.Body.String() == "served"; served != c.served {
			t.Errorf("%s %s: served %v, want %v", c.host, c.path, served, c.served)
		}
		if !c.served && (rec.Code != http.StatusNotFound || rec.Body.String() != unknown.Body.String()) {
			t.Errorf("%s %s: %d %q, want an unknown route's 404", c.host, c.path, rec.Code, rec.Body.String())
		}
	}
	if n := strings.Count(logs.String(), "wrong_host"); n != 1 {
		t.Errorf("wrong Hosts logged %d times in a minute, want once", n)
	}
	// no names: any Host (development)
	rec := httptest.NewRecorder()
	OnlyHosts(slog.New(slog.DiscardHandler), "user", nil)(http.HandlerFunc(ok)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil))
	if rec.Body.String() != "served" {
		t.Error("no user_hosts refused a request")
	}
}

// T04, T05: the console's listener allows no cross-origin request, and a change needs the
// console's Origin and X-Maktaba-Staff: 1.
func TestAdminSameOrigin(t *testing.T) {
	h := AdminSameOrigin("https://admin.maktaba.tv")(http.HandlerFunc(ok))
	do := func(method string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/v1/admin/accounts/1/suspend", nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for name, hdr := range map[string]map[string]string{
		"no Origin":          {StaffHeader: "1"},
		"a wrong Origin":     {"Origin": "https://app.maktaba.tv", StaffHeader: "1"},
		"no X-Maktaba-Staff": {"Origin": "https://admin.maktaba.tv"},
		"X-Maktaba-Staff: 0": {"Origin": "https://admin.maktaba.tv", StaffHeader: "0"},
	} {
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			if rec := do(m, hdr); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"error":"bad_origin"`) {
				t.Errorf("%s %s: %d %s, want 403 bad_origin", name, m, rec.Code, rec.Body.String())
			}
		}
	}
	if rec := do(http.MethodPost, map[string]string{"Origin": "https://admin.maktaba.tv", StaffHeader: "1"}); rec.Body.String() != "served" {
		t.Errorf("from the console: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, nil); rec.Body.String() != "served" {
		t.Errorf("a read: %d", rec.Code)
	}
	rec := do(http.MethodOptions, map[string]string{"Origin": "https://admin.maktaba.tv", "Access-Control-Request-Method": "POST"})
	if rec.Code != http.StatusForbidden || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("a preflight: %d %v, want 403 without CORS headers", rec.Code, rec.Header())
	}
}

// §5: each listener's headers.
func TestListenerHeaders(t *testing.T) {
	for name, c := range map[string]struct {
		h    func(http.Handler) http.Handler
		want map[string]string
	}{
		"user": {APIHeaders, map[string]string{
			"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
			"X-Content-Type-Options":  "nosniff", "X-Frame-Options": "DENY",
		}},
		"admin": {AdminHeaders, map[string]string{
			"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
			"X-Content-Type-Options":  "nosniff", "X-Frame-Options": "DENY",
			"Referrer-Policy": "no-referrer", "Cross-Origin-Opener-Policy": "same-origin",
			"Cross-Origin-Resource-Policy": "same-origin", "Cache-Control": "no-store",
			"X-Robots-Tag": "noindex, nofollow",
		}},
	} {
		rec := httptest.NewRecorder()
		c.h(http.HandlerFunc(ok)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil))
		for k, v := range c.want {
			if got := rec.Header().Get(k); got != v {
				t.Errorf("%s %s = %q, want %q", name, k, got, v)
			}
		}
	}
	rec := httptest.NewRecorder()
	APIHeaders(http.HandlerFunc(ok)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil))
	if rec.Header().Get("X-Robots-Tag") != "" || rec.Header().Get("Cache-Control") != "" {
		t.Error("the user listener took the console's caching or indexing headers")
	}
}

// The relay's own rule reaches the access log (story 50.32 §1, after 50.31).
func TestWithResolvedClientIPReachesTheLog(t *testing.T) {
	var logs bytes.Buffer
	h := WithResolvedClientIP(func(*http.Request) string { return "198.51.100.23" })(
		Logging(slog.New(slog.NewTextHandler(&logs, nil)))(http.HandlerFunc(ok)))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !strings.Contains(logs.String(), "remote_ip=198.51.100.23") {
		t.Fatalf("the log: %s", logs.String())
	}
}
