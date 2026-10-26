package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The home's rate limits and LAN checks read X-Forwarded-For: the relay
// never passes a client's own claim on.
func TestSetForwarded(t *testing.T) {
	cases := []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"a public peer is the client; its claims are dropped", "203.0.113.9:4000",
			[]string{"192.168.1.5"}, "203.0.113.9"},
		{"a local proxy's rightmost entry is the client", "127.0.0.1:5000",
			[]string{"192.168.1.5, 198.51.100.7"}, "198.51.100.7"},
		{"the last header line counts, not the first", "10.0.0.2:5000",
			[]string{"192.168.1.5", "198.51.100.8"}, "198.51.100.8"},
		{"a local proxy with no header is the peer", "127.0.0.1:5000", nil, "127.0.0.1"},
		{"garbage from a proxy falls back to the peer", "127.0.0.1:5000", []string{"nonsense"}, "127.0.0.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://home.relay.test/x", nil)
			r.RemoteAddr = c.remote
			for _, v := range c.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			r.Header.Set("X-Real-Ip", "192.168.1.6")
			r.Header.Set("Forwarded", "for=192.168.1.7")
			r.Header.Set("True-Client-Ip", "192.168.1.8")
			out := r.Header.Clone()
			setForwarded(out, r)
			if got := out.Values("X-Forwarded-For"); len(got) != 1 || got[0] != c.want {
				t.Fatalf("X-Forwarded-For = %v, want [%s]", got, c.want)
			}
			if out.Get("X-Real-Ip") != c.want || out.Get("Forwarded") != "" || out.Get("True-Client-Ip") != "" {
				t.Fatalf("headers: %v", out)
			}
		})
	}
}
