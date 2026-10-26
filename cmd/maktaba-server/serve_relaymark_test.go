package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// The relay agent reaches the home through the web port on loopback: its
// X-Maktaba-Via mark reaches the API from there, and from no other peer.
func TestWebPortKeepsTheRelayMarkFromLoopbackOnly(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "via="+r.Header.Get("X-Maktaba-Via"))
	}))
	defer api.Close()
	var cfg config.Config
	cfg.Server.Listen = strings.TrimPrefix(api.URL, "http://")
	cfg.Server.StreamAddr = "127.0.0.1:1"
	web := webHandler(cfg, nil)

	for _, c := range []struct {
		remote, want string
	}{
		{"127.0.0.1:50000", "via=relay"},
		{"[::1]:50000", "via=relay"},
		{"192.168.1.20:50000", "via="},
		{"203.0.113.9:50000", "via="},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/system/version", nil)
		req.RemoteAddr = c.remote
		req.Header.Set("X-Maktaba-Via", "relay")
		rec := httptest.NewRecorder()
		web.ServeHTTP(rec, req)
		if got := rec.Body.String(); got != c.want {
			t.Errorf("from %s the API saw %q, want %q", c.remote, got, c.want)
		}
	}
}
