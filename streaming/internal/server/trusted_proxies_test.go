package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/clientip"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

// T05 (Epic 59 story 59.08): the public mux believes X-Forwarded-For only from loopback or
// a proxy in [server] trusted_proxies; the routes mounted on it (the front door's, the
// guide's) see the resolved address and still the connection's own (clientip.Peer).
func TestT05ForwardedForFromTrustedPeersOnly(t *testing.T) {
	jwks, _ := auth.NewJWKSCache(context.Background(), "", time.Minute)
	layout := cache.New(t.TempDir())
	_ = layout.EnsureTiers()
	var addr, peer string
	r := New(Deps{
		Cfg: config.Load(), JWKS: jwks,
		Probe:    probe.NewCache(probe.NewFakeBackend(), 16),
		Profiles: capability.NewRegistry(),
		Sessions: session.NewMemoryStore(time.Second),
		Layout:   layout, Files: &fakeOpener{},
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/29")},
		ExtraMounts: []func(chi.Router){func(m chi.Router) {
			m.Get("/t05", func(w http.ResponseWriter, req *http.Request) {
				addr, peer = req.RemoteAddr, clientip.Peer(req)
				w.WriteHeader(http.StatusNoContent)
			})
		}},
	})
	cases := []struct{ name, remote, xff, want string }{
		{"a trusted NAS proxy: its client", "192.168.1.5:41000", "203.0.113.9", "203.0.113.9"},
		{"a TV on the LAN: header ignored", "192.168.1.20:41000", "127.0.0.1", "192.168.1.20:41000"},
		{"the web port's gate through the NAS proxy", "127.0.0.1:41000", "203.0.113.9, 192.168.1.5", "203.0.113.9"},
		{"forged leftmost entries ignored", "192.168.1.5:41000", "10.9.9.9, 6.6.6.6, 203.0.113.9", "203.0.113.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/t05", nil)
			req.RemoteAddr = c.remote
			req.Header.Set("X-Forwarded-For", c.xff)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status %d", rec.Code)
			}
			if addr != c.want || peer != c.remote {
				t.Errorf("RemoteAddr %q peer %q, want %q %q", addr, peer, c.want, c.remote)
			}
		})
	}
}
