package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

type memTunnelStore struct {
	mu     sync.Mutex
	online []bool
}

func (m *memTunnelStore) Authenticate(_ context.Context, id, secret string) (stores.Server, error) {
	if id != "srv-1" || secret != "sek" {
		return stores.Server{}, errors.New("no")
	}
	return stores.Server{ID: "srv-1", Slug: "acme"}, nil
}

func (m *memTunnelStore) SetOnline(_ context.Context, _ string, online bool) error {
	m.mu.Lock()
	m.online = append(m.online, online)
	m.mu.Unlock()
	return nil
}

func (m *memTunnelStore) states() []bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]bool(nil), m.online...)
}

// dialAuth opens a tunnel and sends AUTH with payload; returns the reply.
func dialAuth(t *testing.T, url string, payload any) (*websocket.Conn, Frame) {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(url, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(payload)
	if err := writeControl(c, Frame{Kind: KindAuth, Payload: body}); err != nil {
		t.Fatal(err)
	}
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	f, err := ReadFrame(&byteReader{b: data})
	if err != nil {
		t.Fatal(err)
	}
	return c, f
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Flow control is used only when the agent asks for it; an older agent
// (no "flow") gets an unwindowed tunnel and an AUTH_OK without the field.
func TestServeTunnels_NegotiatesFlowAndRecordsOnline(t *testing.T) {
	reg := NewRegistry()
	store := &memTunnelStore{}
	srv := httptest.NewServer(ServeTunnels(reg, store, nil))
	defer srv.Close()

	c, f := dialAuth(t, srv.URL, AuthPayload{ServerID: "srv-1", Secret: "sek", Flow: FlowVersion})
	var ok map[string]any
	if f.Kind != KindAuthOK || json.Unmarshal(f.Payload, &ok) != nil || ok["flow"] != float64(1) || ok["slug"] != "acme" {
		t.Fatalf("AUTH_OK to a flow agent: kind=%#x %s", f.Kind, f.Payload)
	}
	waitFor(t, "the tunnel", func() bool { _, err := reg.Lookup("acme"); return err == nil })
	tun, _ := reg.Lookup("acme")
	if !tun.FlowControl() {
		t.Fatal("the tunnel is not windowed")
	}
	_ = c.Close()
	waitFor(t, "the tunnel to go", func() bool { _, err := reg.Lookup("acme"); return err != nil })
	waitFor(t, "online then offline", func() bool { s := store.states(); return len(s) == 2 && s[0] && !s[1] })

	c, f = dialAuth(t, srv.URL, map[string]string{"server_id": "srv-1", "secret": "sek"})
	defer c.Close()
	if f.Kind != KindAuthOK || strings.Contains(string(f.Payload), "flow") {
		t.Fatalf("AUTH_OK to an older agent: kind=%#x %s", f.Kind, f.Payload)
	}
	waitFor(t, "the tunnel", func() bool { _, err := reg.Lookup("acme"); return err == nil })
	if tun, _ := reg.Lookup("acme"); tun.FlowControl() {
		t.Fatal("an older agent's tunnel is windowed")
	}
}

func TestServeTunnels_RefusesBadSecret(t *testing.T) {
	reg := NewRegistry()
	srv := httptest.NewServer(ServeTunnels(reg, &memTunnelStore{}, nil))
	defer srv.Close()
	c, f := dialAuth(t, srv.URL, AuthPayload{ServerID: "srv-1", Secret: "wrong"})
	defer c.Close()
	if f.Kind != KindAuthFail {
		t.Fatalf("kind=%#x, want AUTH_FAIL", f.Kind)
	}
	if reg.Len() != 0 {
		t.Fatal("a refused agent was registered")
	}
}

// A replaced tunnel (the agent reconnected) going down is not the server
// going offline.
func TestRegistry_UnregisterOnlyTheLiveTunnel(t *testing.T) {
	reg := NewRegistry()
	a, b := NewTunnel("s", "x", newChanConn()), NewTunnel("s", "x", newChanConn())
	defer b.Close()
	reg.Register("x", a)
	reg.Register("x", b) // closes a
	if reg.Unregister("x", a) {
		t.Fatal("the replaced tunnel unregistered the live one")
	}
	if !reg.Unregister("x", b) {
		t.Fatal("the live tunnel was not unregistered")
	}
}
