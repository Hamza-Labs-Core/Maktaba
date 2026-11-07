package streaming

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Epic 50 story 50.19 §3: the relay's cap counts only on a request the relay agent forwarded,
// less 10 % headroom; a bad value is no cap.
func TestRelayCapFit(t *testing.T) {
	for _, c := range []struct {
		via, cap string
		want     int
	}{
		{"relay", "3000", 2727},
		{"relay", "1500", 1363},
		{"", "3000", 0},       // the LAN, or a direct connection: never capped
		{"direct", "3000", 0}, // direct access: never capped
		{"relay", "", 0},
		{"relay", "0", 0},
		{"relay", "-5", 0},
		{"relay", "99999999999", 0},
		{"relay", "fast", 0},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.via != "" {
			r.Header.Set("X-Maktaba-Via", c.via)
		}
		if c.cap != "" {
			r.Header.Set(RelayCapHeader, c.cap)
		}
		if got := RelayCapFit(r); got != c.want {
			t.Errorf("via %q cap %q: %d, want %d", c.via, c.cap, got, c.want)
		}
	}
}

// A session opened through the relay on a capped plan asks the streaming service for at most
// the cap (it transcodes a file above it); a lower request stays; on the LAN nothing changes;
// a home that can't prepare it answers home_busy.
func TestARelayedSessionFitsTheCap(t *testing.T) {
	h, _, svc := offlineRig(t, true)
	amal := as("u-amal", "lib1")
	openWith := func(body string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/stream/sessions", strings.NewReader(body))
		req = req.WithContext(principal.WithPrincipal(req.Context(), amal))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	relayed := map[string]string{"X-Maktaba-Via": "relay", RelayCapHeader: "3000"}
	last := func() OpenSessionRequest {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		return svc.opened[len(svc.opened)-1]
	}
	if rec := openWith(`{"video_id":"`+vHere+`"}`, relayed); rec.Code != http.StatusCreated || last().MaxBitrateKbps != 2727 {
		t.Fatalf("relayed: %d max %d", rec.Code, last().MaxBitrateKbps)
	}
	if rec := openWith(`{"video_id":"`+vHere+`","max_bitrate_kbps":1000}`, relayed); rec.Code != http.StatusCreated || last().MaxBitrateKbps != 1000 {
		t.Fatalf("a lower request: max %d", last().MaxBitrateKbps)
	}
	if rec := openWith(`{"video_id":"`+vHere+`"}`, map[string]string{RelayCapHeader: "3000"}); rec.Code != http.StatusCreated || last().MaxBitrateKbps != 0 {
		t.Fatalf("on the LAN a cap header is ignored: max %d", last().MaxBitrateKbps)
	}
	rec := openWith(`{"video_id":"`+vBusy+`"}`, relayed)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"home_busy"`) {
		t.Fatalf("busy: %d %s", rec.Code, rec.Body.String())
	}
}
