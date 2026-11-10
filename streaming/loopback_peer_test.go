package main

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/clientip"
)

// Epic 59 story 59.08: the offline station and the supervisor's slot report answer this
// machine only, judged on the connection's own address: a remote client a trusted chain
// resolves to 127.0.0.1 is refused, this machine's caller served.
func TestLoopbackChecksJudgeThePeer(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("192.168.1.5/32")}
	offline := clientip.Middleware(trusted)(loopbackOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	r := chi.NewRouter()
	r.Use(clientip.Middleware(trusted))
	slotReports(nil)(r)
	serve := func(h http.Handler, method, path, peer, xff string) int {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = peer
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := serve(offline, "GET", "/x", "192.168.1.5:5000", "127.0.0.1, 127.0.0.1"); c != http.StatusNotFound {
		t.Fatalf("offline: a remote client passed as loopback: %d", c)
	}
	if c := serve(offline, "GET", "/x", "127.0.0.1:5000", "127.0.0.1, 127.0.0.1"); c != http.StatusNoContent {
		t.Fatalf("offline: this machine refused: %d", c)
	}
	// the slot report: 403 refused; a bad slot name (400) means the check passed
	if c := serve(r, "POST", "/internal/tv/slot-exited/bad!", "192.168.1.5:5000", "127.0.0.1, 127.0.0.1"); c != http.StatusForbidden {
		t.Fatalf("slot report: a remote client passed as loopback: %d", c)
	}
	if c := serve(r, "POST", "/internal/tv/slot-exited/bad!", "127.0.0.1:5000", ""); c != http.StatusBadRequest {
		t.Fatalf("slot report: this machine refused: %d", c)
	}
}
