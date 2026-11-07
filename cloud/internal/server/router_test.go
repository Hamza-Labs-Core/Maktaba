package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
)

// A draining relay (story 50.03) answers /readyz 503 draining before it
// checks anything else, so the edge stops sending it work.
func TestReadyzWhileDraining(t *testing.T) {
	draining := true
	r := NewRouter(slog.New(slog.DiscardHandler), nil, nil, BuildInfo{}, nil, WithDraining(func() bool { return draining }))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"draining"`) {
		t.Errorf("draining: %d %s", rec.Code, rec.Body.String())
	}
	draining = false
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if !strings.Contains(rec.Body.String(), "db_unreachable") {
		t.Errorf("not draining: the database check runs: %d %s", rec.Code, rec.Body.String())
	}
}

// Epic 50 story 50.31: WithTrustedProxies resolves the client IP for every route (the
// access log before them), from CF-Connecting-IP only when the peer is trusted.
func TestRouterResolvesTheClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	for _, c := range []struct {
		name string
		opts []RouterOption
		peer string
		want string
	}{
		{"trusted peer", []RouterOption{WithTrustedProxies(trusted)}, "127.0.0.1:5000", "198.51.100.7"},
		{"untrusted peer", []RouterOption{WithTrustedProxies(trusted)}, "203.0.113.5:5000", "203.0.113.5"},
		{"no option (the relay)", nil, "127.0.0.1:5000", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := NewRouter(slog.New(slog.DiscardHandler), nil, nil, BuildInfo{}, nil, c.opts...)
			var got string
			r.Get("/ip", func(_ http.ResponseWriter, req *http.Request) { got = middleware.GetClientIP(req.Context()) })
			req := httptest.NewRequest(http.MethodGet, "/ip", nil)
			req.RemoteAddr = c.peer
			req.Header.Set("CF-Connecting-IP", "198.51.100.7")
			r.ServeHTTP(httptest.NewRecorder(), req)
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
