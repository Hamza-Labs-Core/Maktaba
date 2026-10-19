package router

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	clusterh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/cluster"
)

// The agent's WebSocket upgrade must survive the canonical middleware
// stack (tracing, logging, metrics and the write guard all wrap the
// ResponseWriter): the handshake reaches the hub, which answers in the
// protocol.
func TestP31AgentUpgradeThroughMiddleware(t *testing.T) {
	r := New(Deps{IPRatePerMin: 600, UserRatePerMin: 600})
	h := &clusterh.Handler{Store: cluster.NewMemoryStore()}
	cfg := agent.Config{HomeAddr: "192.168.1.2", Locator: &agent.Locator{Override: t.TempDir()}}
	hub := MountP31Agent(r, h, P31AgentDeps{Config: cfg})
	if h.OnRevoke == nil || h.OnNodeChanged == nil {
		t.Fatal("phase-1 hooks not wired")
	}
	srv := httptest.NewServer(r)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go hub.Run(ctx)

	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+agent.AgentPath, nil)
	if err != nil {
		t.Fatalf("upgrade through the middleware failed: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"t":"hello","v":99,"kind":"home","nonce":"AAAAAAAAAAAAAAAAAAAAAA=="}`)); err != nil {
		t.Fatal(err)
	}
	_, _, err = c.Read(ctx)
	var ce websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != agent.CloseUnsupportedVersion {
		t.Fatalf("answer = %v", err)
	}
}
