package relay

import (
	"net/http/httptest"
	"testing"
)

// T03 (Epic 50 story 50.31): relay names are DNS only, so a CF-Connecting-IP on one was
// written by the client: the relay never reads it. Its client IP is the peer, or the
// rightmost X-Forwarded-For entry that Caddy (a loopback or private peer) appended.
func TestRelayIgnoresCFConnectingIP(t *testing.T) {
	cases := []struct{ name, peer, cf, xff, want string }{
		{"public peer", "203.0.113.5:443", "198.51.100.7", "", "203.0.113.5"},
		{"Caddy without X-Forwarded-For", "127.0.0.1:51000", "198.51.100.7", "", "127.0.0.1"},
		{"Caddy's appended entry", "127.0.0.1:51000", "198.51.100.7", "6.6.6.6, 198.51.100.20", "198.51.100.20"},
		{"public peer's X-Forwarded-For", "203.0.113.5:443", "", "198.51.100.20", "203.0.113.5"},
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
			if got := clientIP(r); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
			// the access log reads the same rule through ClientIP (story 50.32)
			if got := ClientIP(r); got != c.want {
				t.Errorf("ClientIP: got %q, want %q", got, c.want)
			}
		})
	}
}
