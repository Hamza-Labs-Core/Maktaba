package relaydns

import (
	"context"
	"crypto/x509"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/fakecloudflare"
)

type fixedRelays struct{ rs []relay.PoolRelay }

func (f *fixedRelays) Relays(context.Context) ([]relay.PoolRelay, error) { return f.rs, nil }
func (f *fixedRelays) Beat(context.Context, relay.PoolRelay) error       { return nil }

func setup(t *testing.T) (*fakecloudflare.API, *fixedRelays, *Controller, map[string]bool) {
	t.Helper()
	cf := fakecloudflare.New("zone1", "tok")
	srv := httptest.NewServer(cf)
	t.Cleanup(srv.Close)
	rs := &fixedRelays{}
	answering := map[string]bool{}
	c := &Controller{Relays: rs, DNS: &Cloudflare{BaseURL: srv.URL, ZoneID: "zone1", Token: "tok"},
		Names: []string{"relay.test", "*.relay.test"}, Logger: slog.New(slog.DiscardHandler),
		Probe: func(_ context.Context, r relay.PoolRelay) bool { return answering[r.ID] }}
	return cf, rs, c, answering
}

func member(id, ip string, healthy bool) relay.PoolRelay {
	r := relay.PoolRelay{ID: id, PublicIP: ip, State: relay.RelayActive, CapacityMbps: 1000}
	if !healthy {
		r.Age = relay.PoolHealthyAge + 1
	}
	return r
}

// T06: healthy relays are added to both names (TTL 60); a dead relay (stale heartbeat, or not
// answering its probe) is removed; with every relay dead the last records stay.
func TestReconcile(t *testing.T) {
	cf, rs, c, answering := setup(t)
	ctx := context.Background()
	cf.Seed("relay.test", "203.0.113.1") // the zone as set up: r1 only
	rs.rs = []relay.PoolRelay{member("r1", "203.0.113.1", true), member("r2", "203.0.113.2", true), member("r3", "203.0.113.3", true)}
	answering["r1"], answering["r2"], answering["r3"] = true, true, true
	res, err := c.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"203.0.113.1", "203.0.113.2", "203.0.113.3"}
	for _, name := range c.Names {
		if got := cf.IPs(name); !slices.Equal(got, want) {
			t.Fatalf("%s: %v, want %v", name, got, want)
		}
	}
	if res.Added != 5 || res.Removed != 0 {
		t.Fatalf("round 1: %+v", res)
	}
	for _, r := range cf.Records("*.relay.test") {
		if r.TTL != 60 || r.Proxied {
			t.Fatalf("record %+v: want TTL 60, DNS only", r)
		}
	}
	// r3 dies (its heartbeat stops); r2 heartbeats but no longer answers on its public IP
	rs.rs[2] = member("r3", "203.0.113.3", false)
	answering["r2"] = false
	if _, err := c.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range c.Names {
		if got := cf.IPs(name); !slices.Equal(got, []string{"203.0.113.1"}) {
			t.Fatalf("%s after r2 and r3 went: %v", name, got)
		}
	}
	// the last relay dies: its record stays
	answering["r1"] = false
	res, err = c.Reconcile(ctx)
	if err != nil || !res.Kept {
		t.Fatalf("every relay dead: %+v %v", res, err)
	}
	for _, name := range c.Names {
		if got := cf.IPs(name); !slices.Equal(got, []string{"203.0.113.1"}) {
			t.Fatalf("%s with every relay dead: %v (the last record must stay)", name, got)
		}
	}
	// r2 back: added; r1 stays down and is removed only now that another answers
	answering["r2"] = true
	if _, err := c.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := cf.IPs("relay.test"); !slices.Equal(got, []string{"203.0.113.2"}) {
		t.Fatalf("after r2 came back: %v", got)
	}
}

// The relays table unreadable, or Cloudflare away: nothing is removed (the witness down or
// blind changes nothing).
func TestReconcileKeepsRecordsWhenBlind(t *testing.T) {
	cf, rs, c, answering := setup(t)
	ctx := context.Background()
	cf.Seed("relay.test", "203.0.113.1")
	cf.Seed("relay.test", "203.0.113.2")
	c.Relays = errRelays{}
	if _, err := c.Reconcile(ctx); err == nil {
		t.Fatal("an unreadable relays table: no error")
	}
	if got := cf.IPs("relay.test"); len(got) != 2 {
		t.Fatalf("records changed while blind: %v", got)
	}
	c.Relays = rs
	rs.rs = []relay.PoolRelay{member("r1", "203.0.113.1", true)}
	answering["r1"] = true
	cf.SetDown(true)
	if _, err := c.Reconcile(ctx); err == nil {
		t.Fatal("Cloudflare away: no error")
	}
	cf.SetDown(false)
	if got := cf.IPs("relay.test"); len(got) != 2 {
		t.Fatalf("records changed while Cloudflare was away: %v", got)
	}
	// a wrong token is refused and changes nothing
	c.DNS = &Cloudflare{BaseURL: c.DNS.(*Cloudflare).BaseURL, ZoneID: "zone1", Token: "wrong"}
	if _, err := c.Reconcile(ctx); err == nil {
		t.Fatal("a wrong token: no error")
	}
	if got := cf.IPs("relay.test"); len(got) != 2 {
		t.Fatalf("records changed with a wrong token: %v", got)
	}
}

type errRelays struct{}

func (errRelays) Relays(context.Context) ([]relay.PoolRelay, error) {
	return nil, context.DeadlineExceeded
}
func (errRelays) Beat(context.Context, relay.PoolRelay) error { return nil }

// ProbeReadyz reaches the relay at its IP with the stable name's certificate checked: 200 is
// answering; 503 (draining), a certificate for another name, or nothing listening is not.
func TestProbeReadyz(t *testing.T) {
	code := http.StatusOK
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" || r.Host != "example.com" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(code)
	}))
	defer srv.Close()
	port, _ := strconv.Atoi(srv.URL[strings.LastIndexByte(srv.URL, ':')+1:])
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	at := relay.PoolRelay{PublicIP: "127.0.0.1"}
	if !probeReadyz("example.com", port, 2*time.Second, roots)(context.Background(), at) {
		t.Fatal("a ready relay: not answering")
	}
	code = http.StatusServiceUnavailable
	if probeReadyz("example.com", port, 2*time.Second, roots)(context.Background(), at) {
		t.Fatal("a draining relay (503): answering")
	}
	code = http.StatusOK
	if probeReadyz("relay.test", port, 2*time.Second, roots)(context.Background(), at) {
		t.Fatal("a certificate for another name: answering")
	}
	if ProbeReadyz("example.com", 1, time.Second)(context.Background(), at) {
		t.Fatal("nothing listening: answering")
	}
}
