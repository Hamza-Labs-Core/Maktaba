package frontdoor

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/clientip"
)

// Epic 59 story 59.08: behind the client-address middleware, a chain of trusted hops
// resolves to its leftmost address, which a remote client can write as 127.0.0.1. The
// link-required check judges the connection's own address, so that client stays remote,
// and this machine's own caller stays loopback whatever it forwards.
func TestLoopbackOnlyWhenJudgesThePeer(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("192.168.1.5/32")}
	h := clientip.Middleware(trusted)(LoopbackOnlyWhen(func() bool { return true })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	serve := func(peer, xff string) int {
		req := httptest.NewRequest(http.MethodGet, "/stream/channel/7/live.ts", nil)
		req.RemoteAddr = peer
		req.Header.Set("X-Forwarded-For", xff)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := serve("192.168.1.5:5000", "127.0.0.1, 127.0.0.1"); c != http.StatusForbidden {
		t.Fatalf("a remote client through a trusted proxy passed as loopback: %d", c)
	}
	if c := serve("127.0.0.1:5000", "127.0.0.1, 127.0.0.1"); c != http.StatusNoContent {
		t.Fatalf("this machine's caller refused: %d", c)
	}
	if c := serve("127.0.0.1:5000", "203.0.113.9"); c != http.StatusNoContent {
		t.Fatalf("this machine's proxy refused for its client: %d", c)
	}
}
