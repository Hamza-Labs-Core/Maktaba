package slot

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/clientip"
)

// Epic 59 story 59.08: the slot answers this machine only, judged on the connection's own
// address; a remote client a trusted chain resolves to 127.0.0.1 is refused.
func TestSlotLoopbackJudgesThePeer(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("192.168.1.5/32")}
	h := clientip.Middleware(trusted)((&Server{}).Handler())
	serve := func(peer string) int {
		req := httptest.NewRequest(http.MethodGet, "/internal/tv/slot/none", nil)
		req.RemoteAddr = peer
		req.Header.Set("X-Forwarded-For", "127.0.0.1, 127.0.0.1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := serve("192.168.1.5:5000"); c != http.StatusForbidden {
		t.Fatalf("a remote client passed as loopback: %d", c)
	}
	if c := serve("127.0.0.1:5000"); c == http.StatusForbidden {
		t.Fatalf("this machine refused: %d", c)
	}
	if !Loopback("[::1]:80") || Loopback("192.168.1.5:80") || !Loopback("127.0.0.1") {
		t.Fatal("Loopback")
	}
}
