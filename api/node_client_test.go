package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

type nodeKeys map[string][]byte

func (k nodeKeys) Lookup(_ context.Context, node, _ string) ([]byte, bool) {
	s, ok := k[node]
	return s, ok
}

// Epic 59 story 59.08: the home's public chain as main builds it (delegation.Accept around
// the router, no trusted proxies and no list of nodes) takes the client an approved node
// signed for, and from the same node without that signature, its own address.
func TestHomeChainBelievesSignedNodeClients(t *testing.T) {
	const node = "11111111-2222-3333-4444-555555555555"
	secret := []byte("0123456789abcdef0123456789abcdef")
	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	var addr string
	r.Get("/api/whoami-test", func(w http.ResponseWriter, req *http.Request) {
		addr = req.RemoteAddr
		w.WriteHeader(http.StatusNoContent)
	})
	h := delegation.Accept(&delegation.Verifier{Keys: nodeKeys{node: secret}}, "mkt_sess")(r)
	send := func(attest bool) int {
		req := httptest.NewRequest(http.MethodGet, "http://home/api/whoami-test", nil)
		req.RemoteAddr = "192.168.1.30:41000"
		req.Header.Set("X-Forwarded-For", "192.168.1.77")
		if attest {
			req.Header.Set(delegation.HeaderForwardedClient, "192.168.1.77")
		}
		if err := delegation.SignRequest(req, node, delegation.Key{ID: "dk_1", Secret: secret}, time.Now()); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := send(true); c != http.StatusNoContent || addr != "192.168.1.77" {
		t.Fatalf("signed client: %d %q", c, addr)
	}
	if c := send(false); c != http.StatusNoContent || addr != "192.168.1.30:41000" {
		t.Fatalf("an older node: %d %q", c, addr)
	}
}
