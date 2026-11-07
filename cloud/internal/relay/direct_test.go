package relay

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"
)

type fakeDirect struct {
	mu        sync.Mutex
	endpoints []EndpointsPayload
	from      netip.Addr
	csrs      [][]byte
	checkNow  int
	owed      []Frame
}

func (f *fakeDirect) Endpoints(_ context.Context, _ string, from netip.Addr, p EndpointsPayload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endpoints, f.from = append(f.endpoints, p), from
	return nil
}
func (f *fakeDirect) CSR(_ context.Context, _ string, csr []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.csrs = append(f.csrs, csr)
	return nil
}
func (f *fakeDirect) CheckNow(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkNow++
	return nil
}
func (f *fakeDirect) Deliveries(context.Context, string) ([]Frame, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.owed
	f.owed = nil
	return out, nil
}

// Story 50.15: a home's direct frames reach the handler with the tunnel's source address, and
// what it is owed comes back on the tunnel.
func TestTheDirectFrames(t *testing.T) {
	conn := newChanConn()
	h := &fakeDirect{}
	cert, _ := json.Marshal(CertPayload{Chain: "CHAIN", Domain: "direct-a.example", HomeID: "h"})
	h.owed = []Frame{{Kind: KindDirectCert, Payload: cert}}
	from := netip.MustParseAddr("81.2.3.4")
	tun := NewTunnel("srv", "slug", conn, WithDirect(h, from, nil))
	defer tun.Close()

	ep, _ := json.Marshal(EndpointsPayload{Port: 32443, State: "on", LAN: []string{"192.168.1.20:8443"}})
	conn.in <- Frame{Kind: KindMetaEndpoints, Payload: ep}
	select {
	case f := <-conn.out:
		var c CertPayload
		if f.Kind != KindDirectCert || json.Unmarshal(f.Payload, &c) != nil || c.Chain != "CHAIN" {
			t.Fatalf("delivered %#x %s", f.Kind, f.Payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing delivered")
	}
	h.mu.Lock()
	if len(h.endpoints) != 1 || h.endpoints[0].Port != 32443 || h.from != from {
		t.Fatalf("endpoints %+v from %v", h.endpoints, h.from)
	}
	h.mu.Unlock()

	csr, _ := json.Marshal(CSRPayload{CSR: []byte("DER")})
	conn.in <- Frame{Kind: KindDirectCSR, Payload: csr}
	conn.in <- Frame{Kind: KindDirectCheckNow}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		done := len(h.csrs) == 1 && string(h.csrs[0]) == "DER" && h.checkNow == 1
		h.mu.Unlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("csr %v, check now %d", h.csrs, h.checkNow)
}

// A tunnel with no handler (an older relay, or direct off) ignores the frames.
func TestDirectFramesWithoutAHandlerAreIgnored(t *testing.T) {
	conn := newChanConn()
	tun := NewTunnel("srv", "slug", conn)
	defer tun.Close()
	conn.in <- Frame{Kind: KindDirectCheckNow}
	select {
	case f := <-conn.out:
		t.Fatalf("answered %#x", f.Kind)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTheTunnelsSource(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/relay/ws", nil)
	r.RemoteAddr = "81.2.3.4:50000"
	if tunnelSource(r) != netip.MustParseAddr("81.2.3.4") {
		t.Fatal("a direct peer")
	}
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Add("X-Forwarded-For", "6.6.6.6, 81.2.3.5")
	if tunnelSource(r) != netip.MustParseAddr("81.2.3.5") {
		t.Fatalf("behind Caddy: %v", tunnelSource(r))
	}
}
