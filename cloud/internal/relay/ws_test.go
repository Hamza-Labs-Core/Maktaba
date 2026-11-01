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

// gateStore is a memTunnelStore whose first SetOnline(gate) waits until release is
// closed (a slow database write); entered is closed as that call starts.
type gateStore struct {
	memTunnelStore
	gate    bool
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func newGateStore(gate bool) *gateStore {
	return &gateStore{gate: gate, entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *gateStore) SetOnline(ctx context.Context, id string, online bool) error {
	if online == g.gate {
		first := false
		g.once.Do(func() { first = true })
		if first {
			close(g.entered)
			<-g.release
		}
	}
	return g.memTunnelStore.SetOnline(ctx, id, online)
}

// A tunnel is routable only once its server is recorded online: whatever finds the
// tunnel (the proxy, a test, the dashboard read after it) finds the server online.
func TestServeTunnels_OnlineBeforeRoutable(t *testing.T) {
	reg := NewRegistry()
	store := newGateStore(true)
	srv := httptest.NewServer(ServeTunnels(reg, store, nil))
	defer srv.Close()

	c, f := dialAuth(t, srv.URL, AuthPayload{ServerID: "srv-1", Secret: "sek"})
	defer c.Close()
	if f.Kind != KindAuthOK {
		t.Fatalf("kind=%#x", f.Kind)
	}
	<-store.entered // recording it online, not done yet
	if _, err := reg.Lookup("acme"); err == nil {
		close(store.release)
		t.Fatal("the tunnel is routable while its server is not yet recorded online")
	}
	close(store.release)
	waitFor(t, "the tunnel", func() bool { _, err := reg.Lookup("acme"); return err == nil })
	if s := store.states(); len(s) != 1 || !s[0] {
		t.Fatalf("states when routable: %v", s)
	}
}

// The cloud drops a home's tunnel and the home reconnects while the old tunnel's
// offline is still being written: the server ends online (the old offline never lands
// after the new online).
func TestServeTunnels_ReconnectEndsOnline(t *testing.T) {
	reg := NewRegistry()
	store := newGateStore(false)
	srv := httptest.NewServer(ServeTunnels(reg, store, nil))
	defer srv.Close()

	c1, f := dialAuth(t, srv.URL, AuthPayload{ServerID: "srv-1", Secret: "sek"})
	defer c1.Close()
	if f.Kind != KindAuthOK {
		t.Fatalf("kind=%#x", f.Kind)
	}
	waitFor(t, "the tunnel", func() bool { _, err := reg.Lookup("acme"); return err == nil })
	old, _ := reg.Lookup("acme")
	old.Close()     // the cloud drops it
	<-store.entered // its offline is being written

	c2, f := dialAuth(t, srv.URL, AuthPayload{ServerID: "srv-1", Secret: "sek"})
	defer c2.Close()
	if f.Kind != KindAuthOK {
		close(store.release)
		t.Fatalf("reconnect: kind=%#x", f.Kind)
	}
	// where the reconnect's online can land before the old offline, let it (a second
	// state shows); where it waits its turn none shows, and the bound lets the offline go
	for end := time.Now().Add(300 * time.Millisecond); len(store.states()) < 2 && time.Now().Before(end); {
		time.Sleep(5 * time.Millisecond)
	}
	close(store.release)
	waitFor(t, "the reconnected tunnel", func() bool { tun, err := reg.Lookup("acme"); return err == nil && tun != old })
	waitFor(t, "three states", func() bool { return len(store.states()) == 3 })
	if s := store.states(); !s[0] || s[1] || !s[2] {
		t.Fatalf("states: %v, want online, offline, online (the server ends online)", s)
	}
}
