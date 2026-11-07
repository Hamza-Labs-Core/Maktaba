package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Epic 50 story 50.19: the relay's mark and Maktaba Cloud's bitrate cap reach the API and the
// streaming service only from the relay agent on loopback; a LAN client's own copies are
// dropped, and an unmarked request never carries a cap.
func TestOnlyTheRelayAgentMarksAndCaps(t *testing.T) {
	for _, c := range []struct {
		peer, via, cap   string
		wantVia, wantCap string
	}{
		{"127.0.0.1:5000", "relay", "3000", "relay", "3000"}, // the relay agent
		{"192.168.1.9:5000", "relay", "3000", "", ""},        // a LAN client's claims
		{"127.0.0.1:5000", "", "3000", "", ""},               // not relayed: no cap
		{"127.0.0.1:5000", "direct", "3000", "direct", ""},   // direct access: never capped
		{"[::1]:5000", "relay", "1500", "relay", "1500"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/stream/s1/720p/seg-1.ts", nil)
		r.RemoteAddr = c.peer
		if c.via != "" {
			r.Header.Set("X-Maktaba-Via", c.via)
		}
		r.Header.Set("X-Maktaba-Relay-Cap-Kbps", c.cap)
		keepRelayMark(r)
		keepRelayCap(r)
		if got := r.Header.Get("X-Maktaba-Via"); got != c.wantVia {
			t.Errorf("%s via %q: via %q, want %q", c.peer, c.via, got, c.wantVia)
		}
		if got := r.Header.Get("X-Maktaba-Relay-Cap-Kbps"); got != c.wantCap {
			t.Errorf("%s via %q: cap %q, want %q", c.peer, c.via, got, c.wantCap)
		}
	}
}
