package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

var cloudflared = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}

// T01 (Epic 50 story 50.31): CF-Connecting-IP only from a trusted peer, and only one valid IP.
func TestClientIP(t *testing.T) {
	cases := []struct {
		name, peer, cf, xff, want string
		trusted                   []netip.Prefix
	}{
		{"trusted peer: the header", "127.0.0.1:51000", "198.51.100.7", "", "198.51.100.7", cloudflared},
		{"untrusted peer: the peer", "203.0.113.5:443", "198.51.100.7", "", "203.0.113.5", cloudflared},
		{"no list: the peer", "127.0.0.1:51000", "198.51.100.7", "", "127.0.0.1", nil},
		{"two IPs: the peer", "127.0.0.1:51000", "198.51.100.7, 198.51.100.8", "", "127.0.0.1", cloudflared},
		{"garbage: the peer", "127.0.0.1:51000", "not-an-ip", "", "127.0.0.1", cloudflared},
		{"a zone: the peer", "127.0.0.1:51000", "fe80::1%eth0", "", "127.0.0.1", cloudflared},
		{"IPv6 header through IPv6 loopback", "[::1]:51000", "2001:db8::7", "", "2001:db8::7", cloudflared},
		{"IPv4-mapped peer matches the IPv4 list", "[::ffff:127.0.0.1]:51000", "198.51.100.7", "", "198.51.100.7", cloudflared},
		{"X-Forwarded-For is never read", "127.0.0.1:51000", "", "198.51.100.9", "127.0.0.1", cloudflared},
		{"untrusted IPv6 peer", "[2001:db8::1]:443", "198.51.100.9", "1.2.3.4", "2001:db8::1", cloudflared},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = c.peer
			if c.cf != "" {
				r.Header.Set("CF-Connecting-IP", c.cf)
			}
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if got := ClientIP(r, c.trusted); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestWithClientIPStashesTheResolvedAddress(t *testing.T) {
	var got string
	h := WithClientIP(cloudflared)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = GetClientIP(r.Context())
	}))
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:51000"
	r.Header.Set("CF-Connecting-IP", "198.51.100.7")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != "198.51.100.7" {
		t.Fatalf("got %q", got)
	}
}

// The access log's remote_ip is the resolved address, never a header the client wrote.
func TestLoggingUsesTheResolvedIP(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })

	// with WithClientIP before the logger: the trusted header
	h := WithClientIP(cloudflared)(Logging(logger)(ok))
	r := httptest.NewRequest("GET", "/x", nil)
	r.RemoteAddr = "127.0.0.1:51000"
	r.Header.Set("CF-Connecting-IP", "198.51.100.7")
	r.Header.Set("X-Forwarded-For", "6.6.6.6")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !strings.Contains(buf.String(), `"remote_ip":"198.51.100.7"`) {
		t.Fatalf("log: %s", buf.String())
	}

	// without it (the relay's router): the peer, not X-Forwarded-For
	buf.Reset()
	r = httptest.NewRequest("GET", "/x", nil)
	r.RemoteAddr = "203.0.113.5:443"
	r.Header.Set("X-Forwarded-For", "6.6.6.6")
	r.Header.Set("CF-Connecting-IP", "6.6.6.7")
	Logging(logger)(ok).ServeHTTP(httptest.NewRecorder(), r)
	if !strings.Contains(buf.String(), `"remote_ip":"203.0.113.5"`) {
		t.Fatalf("log: %s", buf.String())
	}
}
