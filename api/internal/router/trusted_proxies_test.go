package router

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

// T05 (Epic 59 story 59.08): the API believes X-Forwarded-For only from loopback or a proxy
// in [server] trusted_proxies, and judges this machine's own callers and the relay's mark
// on the real peer, before the address is resolved.
func TestT05ForwardedForFromTrustedPeersOnly(t *testing.T) {
	nas := []netip.Prefix{netip.MustParsePrefix("192.168.1.5/32")}
	r := New(Deps{IdempotencyStore: idempotency.NewMemoryStore(), TrustedProxies: nas})
	var addr string
	var local bool
	r.Get("/api/t05", func(w http.ResponseWriter, req *http.Request) {
		addr, local = req.RemoteAddr, mw.LocalCaller(req)
		w.WriteHeader(http.StatusNoContent)
	})
	cases := []struct {
		name, peer, xff, want string
		local                 bool
	}{
		{"a trusted NAS proxy: its client", "192.168.1.5:41000", "203.0.113.9", "203.0.113.9", false},
		{"an untrusted LAN peer: header ignored", "192.168.1.20:41000", "203.0.113.9", "192.168.1.20:41000", false},
		{"an untrusted LAN peer cannot claim loopback", "192.168.1.20:41000", "127.0.0.1", "192.168.1.20:41000", false},
		{"the web port on loopback, through the NAS proxy", "127.0.0.1:41000", "203.0.113.9, 192.168.1.5", "203.0.113.9", false},
		{"forged leftmost entries ignored", "192.168.1.5:41000", "127.0.0.1, 6.6.6.6, 203.0.113.9", "203.0.113.9", false},
		{"the web port for this machine", "127.0.0.1:41000", "127.0.0.1", "127.0.0.1", true},
		{"no header: the peer", "192.168.1.5:41000", "", "192.168.1.5:41000", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/t05", nil)
			req.RemoteAddr = c.peer
			if c.xff != "" {
				req.Header.Set("X-Forwarded-For", c.xff)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status %d", rec.Code)
			}
			if addr != c.want || local != c.local {
				t.Errorf("RemoteAddr %q local %v, want %q %v", addr, local, c.want, c.local)
			}
		})
	}

	// with no list the NAS proxy is a LAN peer like any other
	r = New(Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	r.Get("/api/t05", func(_ http.ResponseWriter, req *http.Request) { addr = req.RemoteAddr })
	req := httptest.NewRequest(http.MethodGet, "/api/t05", nil)
	req.RemoteAddr = "192.168.1.5:41000"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	r.ServeHTTP(httptest.NewRecorder(), req)
	if addr != "192.168.1.5:41000" {
		t.Fatalf("empty list: %q", addr)
	}
}

// The per-IP limit keys on the resolved address: a LAN client rotating X-Forwarded-For
// stays one bucket.
func TestT05RateLimitKeyIgnoresForgedHeaders(t *testing.T) {
	r := New(Deps{IdempotencyStore: idempotency.NewMemoryStore(), IPRatePerMin: 2})
	r.Get("/api/t05", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	codes := []int{}
	for _, xff := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4"} {
		req := httptest.NewRequest(http.MethodGet, "/api/t05", nil)
		req.RemoteAddr = "192.168.1.20:41000"
		req.Header.Set("X-Forwarded-For", xff)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	if codes[len(codes)-1] != http.StatusTooManyRequests {
		t.Fatalf("a forged X-Forwarded-For got its own bucket: %v", codes)
	}
}
