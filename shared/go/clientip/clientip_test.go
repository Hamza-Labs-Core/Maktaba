package clientip

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestParseList(t *testing.T) {
	got, err := ParseList([]string{"192.168.1.5", " 10.0.0.0/8 ", "", "2001:db8::1", "::ffff:192.168.1.6", "192.168.1.77/24", "::ffff:10.1.0.0/112"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"192.168.1.5/32", "10.0.0.0/8", "2001:db8::1/128", "192.168.1.6/32", "192.168.1.0/24", "10.1.0.0/16"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("entry %d: got %s, want %s", i, got[i], want[i])
		}
	}
	for _, bad := range []string{"nas.local", "192.168.1.300", "10.0.0.0/33", "fe80::1%eth0", "1.2.3.4:80"} {
		_, err := ParseList([]string{"10.0.0.1", bad})
		if err == nil || !strings.Contains(err.Error(), "entry 1") {
			t.Errorf("%q: err %v", bad, err)
		}
	}
	if ps, err := ParseList(nil); err != nil || len(ps) != 0 {
		t.Fatalf("empty: %v %v", ps, err)
	}
}

func TestParseCIDRsRefusesABareAddress(t *testing.T) {
	if _, err := ParseCIDRs([]string{"127.0.0.1"}); err == nil || !strings.Contains(err.Error(), "is not a CIDR") {
		t.Fatalf("bare address: %v", err)
	}
	ps, err := ParseCIDRs([]string{" 10.1.2.3/8 "})
	if err != nil || ps[0].String() != "10.0.0.0/8" {
		t.Fatalf("%v %v", ps, err)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv(EnvTrustedProxies, "192.168.1.5, 10.0.0.0/8,")
	ps, err := FromEnv()
	if err != nil || len(ps) != 2 {
		t.Fatalf("%v %v", ps, err)
	}
	t.Setenv(EnvTrustedProxies, "nas")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), EnvTrustedProxies) {
		t.Fatalf("bad value: %v", err)
	}
	t.Setenv(EnvTrustedProxies, "")
	if ps, err := FromEnv(); err != nil || len(ps) != 0 {
		t.Fatalf("empty: %v %v", ps, err)
	}
}

var nas = []netip.Prefix{netip.MustParsePrefix("192.168.1.5/32")}

// T05 (Epic 59 story 59.08): the home's resolver.
func TestForwarded(t *testing.T) {
	cases := []struct {
		name, peer, xff, realIP, want string
		used                          bool
		trusted                       []netip.Prefix
	}{
		{"no header: the peer", "192.168.1.20:5000", "", "", "192.168.1.20", false, nas},
		{"untrusted peer: header ignored", "192.168.1.20:5000", "203.0.113.9", "", "192.168.1.20", false, nas},
		{"untrusted peer: X-Real-IP ignored", "192.168.1.20:5000", "", "127.0.0.1", "192.168.1.20", false, nas},
		{"untrusted peer, empty list", "192.168.1.5:5000", "203.0.113.9", "", "192.168.1.5", false, nil},
		{"trusted NAS proxy: its client", "192.168.1.5:5000", "203.0.113.9", "", "203.0.113.9", true, nas},
		{"trusted NAS proxy: forged leftmost ignored", "192.168.1.5:5000", "127.0.0.1, 6.6.6.6, 203.0.113.9", "", "203.0.113.9", true, nas},
		{"loopback: always trusted", "127.0.0.1:5000", "192.168.1.20", "", "192.168.1.20", true, nil},
		{"loopback through the NAS proxy", "127.0.0.1:5000", "203.0.113.9, 192.168.1.5", "", "203.0.113.9", true, nas},
		{"loopback through an untrusted hop", "127.0.0.1:5000", "203.0.113.9, 192.168.1.20", "", "192.168.1.20", true, nas},
		{"every hop trusted: the leftmost", "[::1]:5000", "127.0.0.1, 192.168.1.5", "", "127.0.0.1", true, nas},
		{"two header lines are one list", "127.0.0.1:5000", "6.6.6.6|203.0.113.9", "", "203.0.113.9", true, nil},
		{"garbage stops the walk", "127.0.0.1:5000", "203.0.113.9, nonsense", "", "127.0.0.1", false, nil},
		{"garbage behind a trusted hop", "127.0.0.1:5000", "nonsense, 192.168.1.5", "", "192.168.1.5", true, nas},
		{"an address with a port", "127.0.0.1:5000", "[2001:db8::9]:443", "", "2001:db8::9", true, nil},
		{"X-Real-IP from a trusted peer", "192.168.1.5:5000", "", "203.0.113.9", "203.0.113.9", true, nas},
		{"bad X-Real-IP: the peer", "192.168.1.5:5000", "", "a, b", "192.168.1.5", false, nas},
		{"IPv4-mapped peer", "[::ffff:192.168.1.5]:5000", "203.0.113.9", "", "203.0.113.9", true, nas},
		{"bare RemoteAddr", "127.0.0.1", "203.0.113.9", "", "203.0.113.9", true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = c.peer
			for _, v := range strings.Split(c.xff, "|") {
				if v != "" {
					r.Header.Add("X-Forwarded-For", v)
				}
			}
			if c.realIP != "" {
				r.Header.Set("X-Real-IP", c.realIP)
			}
			ip, used := Forwarded(r, c.trusted)
			if ip.String() != c.want || used != c.used {
				t.Errorf("got %s %v, want %s %v", ip, used, c.want, c.used)
			}
			if got := Client(r, c.trusted); got != c.want {
				t.Errorf("Client: %q", got)
			}
		})
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "not an address"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := Client(r, nil); got != "" {
		t.Fatalf("no peer: %q", got)
	}
}

func TestMiddleware(t *testing.T) {
	var addr, peer string
	h := Middleware(nas)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		addr, peer = r.RemoteAddr, Peer(r)
	}))
	serve := func(remote, xff string) {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	serve("192.168.1.5:5000", "203.0.113.9")
	if addr != "203.0.113.9" || peer != "192.168.1.5:5000" {
		t.Fatalf("trusted: %q %q", addr, peer)
	}
	serve("192.168.1.20:5000", "127.0.0.1")
	if addr != "192.168.1.20:5000" || peer != addr {
		t.Fatalf("untrusted: %q %q", addr, peer)
	}
	serve("127.0.0.1:5000", "")
	if addr != "127.0.0.1:5000" || peer != addr {
		t.Fatalf("no header: %q %q", addr, peer)
	}
}

// The cloud's resolver (Epic 50 story 50.31 T01; cloud/internal/middleware keeps its table).
func TestCloudflare(t *testing.T) {
	cloudflared := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:51000"
	r.Header.Set("CF-Connecting-IP", "198.51.100.7")
	r.Header.Set("X-Forwarded-For", "6.6.6.6")
	if got := Cloudflare(r, cloudflared); got != "198.51.100.7" {
		t.Fatalf("trusted: %q", got)
	}
	if got := Cloudflare(r, nil); got != "127.0.0.1" {
		t.Fatalf("no list: %q", got)
	}
	r.RemoteAddr = "203.0.113.5:443"
	if got := Cloudflare(r, cloudflared); got != "203.0.113.5" {
		t.Fatalf("untrusted: %q", got)
	}
}

func TestHost(t *testing.T) {
	for in, want := range map[string]string{"192.168.1.5:80": "192.168.1.5", "[::1]:80": "::1", "10.0.0.1": "10.0.0.1", "x": ""} {
		if got := Host(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
