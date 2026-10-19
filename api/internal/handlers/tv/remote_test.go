package tv

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

// infoServer serves /api/tv/info behind the API's StripVia middleware, with a
// remote controller reporting avail.
func infoServer(t *testing.T, avail RemoteAvailability) http.Handler {
	t.Helper()
	db, _ := newServer(t, "http://192.168.1.20:8782")
	r := chi.NewRouter()
	r.Use(mw.StripVia)
	(&Handler{DB: db, PublicBaseURL: "http://192.168.1.20:8782", Now: testNow,
		Remote: func(context.Context) RemoteAvailability { return avail }}).Mount(r)
	return r
}

func remoteOf(t *testing.T, srv http.Handler, remoteAddr string, hdr map[string]string) RemoteInfo {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/tv/info", nil)
	req.RemoteAddr = remoteAddr
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	req = req.WithContext(principal.WithPrincipal(req.Context(), user))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("info: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Remote RemoteInfo `json:"remote"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Remote
}

var ready = RemoteAvailability{Available: true, MaxStreams: 2, TokenTTLMinutes: 60, Detection: DetectRelay}

func TestInfoViaRelayHeader(t *testing.T) {
	srv := infoServer(t, ready)
	got := remoteOf(t, srv, "127.0.0.1:41000", map[string]string{ViaHeader: "relay"})
	if !got.ViaRelay || !got.Available || got.MaxStreams != 2 || got.TokenTTLMinutes != 60 || got.Reason != nil {
		t.Fatalf("relay call: %+v", got)
	}
	if got := remoteOf(t, srv, "127.0.0.1:41000", nil); got.ViaRelay {
		t.Fatalf("a loopback call without the header is a LAN call: %+v", got)
	}
}

func TestInfoStripsSpoofedHeader(t *testing.T) {
	srv := infoServer(t, ready)
	for _, addr := range []string{"192.168.1.30:5000", "203.0.113.9:5000", "[fd00::5]:5000"} {
		if got := remoteOf(t, srv, addr, map[string]string{ViaHeader: "relay"}); got.ViaRelay {
			t.Errorf("%s: a client's own X-Maktaba-Via counted: %+v", addr, got)
		}
	}
}

func TestPublicIPDetection(t *testing.T) {
	relayOnly := infoServer(t, ready)
	if got := remoteOf(t, relayOnly, "203.0.113.9:5000", nil); got.ViaRelay {
		t.Fatalf("detection relay: a public address must not count: %+v", got)
	}
	pub := ready
	pub.Detection = DetectRelayOrPublicIP
	srv := infoServer(t, pub)
	for addr, want := range map[string]bool{
		"203.0.113.9:5000":      true,
		"[2001:db8::1]:5000":    true,
		"192.168.1.30:5000":     false,
		"10.0.0.4:5000":         false,
		"172.16.3.3:5000":       false,
		"100.72.1.1:5000":       false, // CGNAT
		"[fd12::1]:5000":        false, // ULA
		"127.0.0.1:5000":        false,
		"[fe80::1%25eth0]:5000": false,
		"169.254.10.10:5000":    false,
	} {
		if got := remoteOf(t, srv, addr, nil); got.ViaRelay != want {
			t.Errorf("%s: via_relay %v, want %v", addr, got.ViaRelay, want)
		}
	}
}

func TestInfoRemoteUnavailableReason(t *testing.T) {
	srv := infoServer(t, RemoteAvailability{Reason: "remote viewing is switched off", MaxStreams: 2, TokenTTLMinutes: 60})
	got := remoteOf(t, srv, "127.0.0.1:1", map[string]string{ViaHeader: "relay"})
	if got.Available || got.Reason == nil || *got.Reason != "remote viewing is switched off" || !got.ViaRelay {
		t.Fatalf("unavailable: %+v", got)
	}
	// no controller at all: unavailable with a reason
	db, _ := newServer(t, "")
	r := chi.NewRouter()
	(&Handler{DB: db, Now: testNow}).Mount(r)
	if got := remoteOf(t, r, "127.0.0.1:1", nil); got.Available || got.Reason == nil {
		t.Fatalf("no controller: %+v", got)
	}
}

func TestPublicIP(t *testing.T) {
	for s, want := range map[string]bool{"8.8.8.8": true, "100.63.255.255": true, "100.64.0.1": false, "::1": false} {
		if got := PublicIP(net.ParseIP(s)); got != want {
			t.Errorf("%s: %v", s, got)
		}
	}
}

func TestInfoHostsOnAir(t *testing.T) {
	db, srv := newServer(t, "")
	beat := testNow().Add(-5e9).Format("2006-01-02T15:04:05Z")
	old := testNow().Add(-5 * 60e9).Format("2006-01-02T15:04:05Z")
	for _, q := range []string{
		`INSERT INTO streaming_hosts (host_id, host, active_channels, heartbeat_at) VALUES ('h1','mac',3,'` + beat + `')`,
		`INSERT INTO streaming_hosts (host_id, host, active_channels, heartbeat_at) VALUES ('h2','pc',0,'` + beat + `')`,
		`INSERT INTO streaming_hosts (host_id, host, active_channels, heartbeat_at) VALUES ('h3','old',2,'` + old + `')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_, out := do(t, srv, "GET", "/api/tv/info", "", user)
	cl, _ := out["cluster"].(map[string]any)
	if cl["hosts_on_air"] != float64(1) {
		t.Fatalf("cluster: %v", out["cluster"])
	}
}
