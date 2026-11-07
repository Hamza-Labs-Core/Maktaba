package cloudlink

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
)

// cloudDirect is the cloud's side in these tests: what a relay's DirectHandler is told and owes.
type cloudDirect struct {
	mu        sync.Mutex
	endpoints []EndpointsPayload
	from      netip.Addr
	csrs      [][]byte
	checkNow  int
}

func (c *cloudDirect) Endpoints(_ context.Context, _ string, from netip.Addr, p EndpointsPayload) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.endpoints, c.from = append(c.endpoints, p), from
	return nil
}

func (c *cloudDirect) CSR(_ context.Context, _ string, csr []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.csrs = append(c.csrs, csr)
	return nil
}

func (c *cloudDirect) CheckNow(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkNow++
	return nil
}

func (c *cloudDirect) Name(context.Context, string) (NamePayload, error) {
	return NamePayload{Domain: "direct-a.example", HomeID: "0123abcd"}, nil
}

// Deliveries: a chain once a CSR came, and the check result after Check now.
func (c *cloudDirect) Deliveries(context.Context, string) ([]Frame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Frame
	if len(c.csrs) > 0 {
		b, _ := json.Marshal(CertPayload{Chain: "CHAIN", Domain: "direct-a.example", HomeID: "0123abcd"})
		out = append(out, Frame{Kind: KindDirectCert, Payload: b})
	}
	if c.checkNow > 0 {
		b, _ := json.Marshal(CheckPayload{Result: "ok", Port: 32443, At: time.Now()})
		out = append(out, Frame{Kind: KindDirectCheck, Payload: b})
	}
	return out, nil
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Story 50.15: the home's API tells the agent its endpoints, asks its name and sends its CSR
// over the admin port; the agent carries them to the real cloud tunnel and keeps the name,
// the chain and the check result for the API. What was posted before the tunnel was up is
// said on connect.
func TestTheAgentCarriesTheDirectFrames(t *testing.T) {
	cloud := &cloudDirect{}
	cloudEnd, homeEnd := newPipe()
	tun := relay.NewTunnel("srv-1", "acme", cloudEnd, relay.WithDirect(cloud, netip.MustParseAddr("81.2.3.4"), nil))
	defer tun.Close()

	d := &Direct{}
	admin := httptest.NewServer(DirectHandler(d))
	defer admin.Close()
	post := func(body DirectRequest) int {
		b, _ := json.Marshal(body)
		resp, err := http.Post(admin.URL, "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	status := func() DirectStatus {
		resp, err := http.Get(admin.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var st DirectStatus
		if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
			t.Fatal(err)
		}
		return st
	}

	// before the tunnel: kept; Check now can't be done
	if code := post(DirectRequest{Endpoints: &EndpointsPayload{Port: 32443, State: "on"}, AskName: true}); code != http.StatusNoContent {
		t.Fatalf("post offline: %d", code)
	}
	if code := post(DirectRequest{CheckNow: true}); code != http.StatusServiceUnavailable {
		t.Fatalf("check now offline: %d", code)
	}
	if st := status(); st.Online || st.Domain != "" {
		t.Fatalf("offline status %+v", st)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dialed := make(chan struct{}, 1)
	sup := &Supervisor{
		Proxy:  &LocalProxy{BaseURL: "http://127.0.0.1:1"},
		Direct: d,
		dial: func(ctx context.Context) (DialResult, error) {
			select {
			case dialed <- struct{}{}:
				return DialResult{Conn: homeEnd, Slug: "acme", Direct: true}, nil
			default:
				<-ctx.Done()
				return DialResult{}, ctx.Err()
			}
		},
	}
	done := make(chan struct{})
	go func() { _ = sup.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	waitFor(t, "the name", func() bool {
		st := status()
		return st.Online && st.Domain == "direct-a.example" && st.HomeID == "0123abcd"
	})
	// the endpoints frame can arrive after the name: wait for it too (it raced, #861's CI)
	waitFor(t, "the endpoints", func() bool {
		cloud.mu.Lock()
		defer cloud.mu.Unlock()
		return len(cloud.endpoints) == 1
	})
	cloud.mu.Lock()
	eps, from := append([]EndpointsPayload(nil), cloud.endpoints...), cloud.from
	cloud.mu.Unlock()
	if eps[0].Port != 32443 || from != netip.MustParseAddr("81.2.3.4") {
		t.Fatalf("endpoints %+v from %v", eps, from)
	}

	if code := post(DirectRequest{CSR: []byte("DER")}); code != http.StatusNoContent {
		t.Fatalf("csr: %d", code)
	}
	waitFor(t, "the chain", func() bool { st := status(); return st.Chain == "CHAIN" && !st.Pending })
	if code := post(DirectRequest{CheckNow: true}); code != http.StatusNoContent {
		t.Fatalf("check now: %d", code)
	}
	waitFor(t, "the check", func() bool {
		st := status()
		return st.Check != nil && st.Check.Result == "ok" && st.Check.Port == 32443
	})
}

// An older cloud (it didn't agree to direct): nothing is sent, and the status says offline.
func TestAnOlderCloudGetsNoDirectFrames(t *testing.T) {
	cloudEnd, homeEnd := newPipe()
	d := &Direct{}
	if err := d.Post(DirectRequest{AskName: true}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	dialed := make(chan struct{}, 1)
	sup := &Supervisor{
		Proxy:  &LocalProxy{BaseURL: "http://127.0.0.1:1"},
		Direct: d,
		dial: func(ctx context.Context) (DialResult, error) {
			select {
			case dialed <- struct{}{}:
				return DialResult{Conn: homeEnd, Slug: "acme"}, nil
			default:
				<-ctx.Done()
				return DialResult{}, ctx.Err()
			}
		},
	}
	done := make(chan struct{})
	go func() { _ = sup.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case f := <-cloudEnd.in:
		t.Fatalf("sent %#x to an older cloud", f.Kind)
	case <-time.After(300 * time.Millisecond):
	}
	if d.Status().Online {
		t.Fatal("online with an older cloud")
	}
}
