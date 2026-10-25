package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	chimw "github.com/go-chi/chi/v5/middleware"
)

func TestStripVia(t *testing.T) {
	cases := []struct {
		remote string
		keep   bool
	}{
		{"127.0.0.1:5555", true},
		{"[::1]:5555", true},
		{"192.168.1.20:5555", false},
		{"8.8.8.8:443", false},
		{"garbage", false},
	}
	for _, c := range cases {
		var got string
		h := StripVia(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Header.Get(ViaHeader) }))
		req := httptest.NewRequest("GET", "/api/tv/info", nil)
		req.RemoteAddr = c.remote
		req.Header.Set(ViaHeader, "relay")
		h.ServeHTTP(httptest.NewRecorder(), req)
		if (got == "relay") != c.keep {
			t.Errorf("%s: header %q, keep %v", c.remote, got, c.keep)
		}
	}
}

// A program on this machine is told apart on the real peer address, before RealIP: through
// the web port's proxy (its client in X-Forwarded-For) only when that client is loopback
// too, never the relay's calls, never a LAN client that forges a forwarding header.
func TestMarkLocalCaller(t *testing.T) {
	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		local   bool
	}{
		{"loopback", "127.0.0.1:5555", nil, true},
		{"loopback v6", "[::1]:5555", nil, true},
		{"the LAN", "192.168.1.20:5555", nil, false},
		{"garbage", "garbage", nil, false},
		{"the web port, for this machine", "127.0.0.1:5555", map[string]string{"X-Forwarded-For": "127.0.0.1"}, true},
		{"the web port, for the LAN", "127.0.0.1:5555", map[string]string{"X-Forwarded-For": "192.168.1.20"}, false},
		{"the web port, a forged loopback first", "127.0.0.1:5555", map[string]string{"X-Forwarded-For": "127.0.0.1, 192.168.1.20"}, false},
		{"the web port, a forged X-Real-IP", "127.0.0.1:5555", map[string]string{"X-Real-IP": "127.0.0.1", "X-Forwarded-For": "192.168.1.20"}, false},
		{"a Forwarded header", "127.0.0.1:5555", map[string]string{"Forwarded": "for=127.0.0.1"}, false},
		{"the relay", "127.0.0.1:5555", map[string]string{ViaHeader: "relay"}, false},
		{"the LAN, a forged X-Real-IP", "192.168.1.20:5555", map[string]string{"X-Real-IP": "127.0.0.1"}, false},
		{"the LAN, a forged X-Forwarded-For", "192.168.1.20:5555", map[string]string{"X-Forwarded-For": "127.0.0.1"}, false},
	}
	for _, c := range cases {
		var got bool
		// as the router chains it: before RealIP, which rewrites RemoteAddr from the headers
		h := MarkLocalCaller(chimw.RealIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = LocalCaller(r) }))) //nolint:staticcheck // SA1019: as the router chains it
		req := httptest.NewRequest("GET", "/api/cluster/self", nil)
		req.RemoteAddr = c.remote
		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		if got != c.local {
			t.Errorf("%s: local %v, want %v", c.name, got, c.local)
		}
	}
	if LocalCaller(httptest.NewRequest("GET", "/", nil)) {
		t.Error("a request MarkLocalCaller did not see counted as local")
	}
}
