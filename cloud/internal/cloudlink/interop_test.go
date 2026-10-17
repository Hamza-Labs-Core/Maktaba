package cloudlink

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
)

// readBodyWithKeepalive reads resp.Body to EOF. The cloud relay keeps
// the stream registered until its body is consumed, so RESPONSE_BODY
// frames arriving after Tunnel.Proxy returns are still delivered.
func readBodyWithKeepalive(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// TestEndToEnd_RealRelayTunnel_HeaderRoundTrip proves the cloudlink
// client speaks the EXACT protocol the cloud relay implements: it
// wires the production cloud-side relay.Tunnel (the same type
// relay/ws.go constructs after AUTH_OK) to the production
// cloudlink.Multiplexer over an in-memory pipe and drives a real
// request via Tunnel.Proxy (the production call site).
func TestEndToEnd_RealRelayTunnel_HeaderRoundTrip(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/books" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("X-Test"); got != "abc" {
			t.Errorf("forwarded header lost: X-Test=%q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "payload" {
			t.Errorf("forwarded body = %q, want payload", string(body))
		}
		w.Header().Set("X-Echo", "pong")
		w.WriteHeader(http.StatusCreated)
	}))
	defer local.Close()

	cloudEnd, clientEnd := newPipe()
	tun := relay.NewTunnel("srv-1", "acme", cloudEnd)
	defer tun.Close()
	mux := NewMultiplexer(clientEnd, &LocalProxy{BaseURL: local.URL})
	defer mux.Close()

	req := httptest.NewRequest(http.MethodPost, "http://acme.maktaba.app/api/books",
		strings.NewReader("payload"))
	req.Host = "acme.maktaba.app"
	req.Header.Set("X-Test", "abc")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := tun.Proxy(ctx, req)
	if err != nil {
		t.Fatalf("Tunnel.Proxy: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	if resp.Header.Get("X-Echo") != "pong" {
		t.Fatalf("response header X-Echo = %q, want pong", resp.Header.Get("X-Echo"))
	}
}

// TestEndToEnd_LocalAPIError_PropagatesStatus ensures a non-2xx from
// the loopback API is faithfully relayed (not swallowed into a 502).
// Status travels in the HEAD frame, so this is deterministic.
func TestEndToEnd_LocalAPIError_PropagatesStatus(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer local.Close()

	cloudEnd, clientEnd := newPipe()
	tun := relay.NewTunnel("srv-2", "beta", cloudEnd)
	defer tun.Close()
	mux := NewMultiplexer(clientEnd, &LocalProxy{BaseURL: local.URL})
	defer mux.Close()

	req := httptest.NewRequest(http.MethodGet, "http://beta.maktaba.app/x", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := tun.Proxy(ctx, req)
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 propagated", resp.StatusCode)
	}
}

// TestEndToEnd_LoopbackDown_ReturnsBadGateway verifies the client emits
// a well-formed 502 (not a hung stream) when its own API is unreachable.
func TestEndToEnd_LoopbackDown_ReturnsBadGateway(t *testing.T) {
	cloudEnd, clientEnd := newPipe()
	tun := relay.NewTunnel("srv-3", "gamma", cloudEnd)
	defer tun.Close()
	mux := NewMultiplexer(clientEnd, &LocalProxy{
		BaseURL: "http://127.0.0.1:1",
		Client:  &http.Client{Timeout: 2 * time.Second},
	})
	defer mux.Close()

	req := httptest.NewRequest(http.MethodGet, "http://gamma.maktaba.app/x", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := tun.Proxy(ctx, req)
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

// TestEndToEnd_PingPong confirms a cloud-originated PING is answered
// with PONG by the multiplexer over the real codec.
func TestEndToEnd_PingPong(t *testing.T) {
	cloudEnd, clientEnd := newPipe()
	mux := NewMultiplexer(clientEnd, &LocalProxy{BaseURL: "http://127.0.0.1:1"})
	defer mux.Close()

	if err := cloudEnd.WriteFrame(Frame{Kind: KindPing, StreamID: 99}); err != nil {
		t.Fatalf("write PING: %v", err)
	}
	got, err := cloudEnd.ReadFrame()
	if err != nil {
		t.Fatalf("read PONG: %v", err)
	}
	if got.Kind != KindPong || got.StreamID != 99 {
		t.Fatalf("got kind=%#x stream=%d, want PONG/99", got.Kind, got.StreamID)
	}
}

// TestEndToEnd_BodyRoundTrip_HappyPath asserts a full body round-trip
// through the REAL cloud relay.Tunnel, including body frames that the
// client emits after Tunnel.Proxy has already returned the response head.
func TestEndToEnd_BodyRoundTrip_HappyPath(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"echo":"` + string(body) + `"}`))
	}))
	defer local.Close()

	cloudEnd, clientEnd := newPipe()
	tun := relay.NewTunnel("srv-4", "delta", cloudEnd)
	defer tun.Close()
	mux := NewMultiplexer(clientEnd, &LocalProxy{BaseURL: local.URL})
	defer mux.Close()

	req := httptest.NewRequest(http.MethodPost, "http://delta.maktaba.app/x",
		strings.NewReader("payload"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := tun.Proxy(ctx, req)
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := readBodyWithKeepalive(t, resp); got != `{"echo":"payload"}` {
		t.Fatalf("body = %q, want echo of payload", got)
	}
}
