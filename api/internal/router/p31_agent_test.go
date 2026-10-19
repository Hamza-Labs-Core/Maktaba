package router

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	clusterh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/cluster"
)

func startHub(t *testing.T, cfg agent.Config) (*httptest.Server, *clusterh.Handler) {
	t.Helper()
	r := New(Deps{IPRatePerMin: 600, UserRatePerMin: 600})
	h := &clusterh.Handler{Store: cluster.NewMemoryStore()}
	hub := MountP31Agent(r, h, P31AgentDeps{Config: cfg})
	srv := httptest.NewServer(r)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); hub.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; srv.Close() })
	return srv, h
}

// The agent's WebSocket upgrade must survive the canonical middleware
// stack (tracing, logging, metrics and the write guard all wrap the
// ResponseWriter): the handshake reaches the hub, which answers in the
// protocol.
func TestP31AgentUpgradeThroughMiddleware(t *testing.T) {
	srv, h := startHub(t, agent.Config{HomeAddr: "192.168.1.2", Locator: &agent.Locator{Override: t.TempDir()}})
	if h.OnRevoke == nil || h.OnNodeChanged == nil {
		t.Fatal("phase-1 hooks not wired")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
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

// Behind a reverse proxy every process arrives from the proxy's address:
// the hub keys its handshake limit and a machine's folder on the client
// address the API's chain resolves (chi RealIP: X-Forwarded-For), not on
// the proxy's.
func TestP31AgentClientAddressBehindProxy(t *testing.T) {
	dir := t.TempDir()
	srv, _ := startHub(t, agent.Config{HomeAddr: "192.168.1.2", Locator: &agent.Locator{Override: dir},
		AgentRatePerMin: 2})
	hello := func(xff string) websocket.StatusCode {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+agent.AgentPath,
			&websocket.DialOptions{HTTPHeader: http.Header{"X-Forwarded-For": {xff}}})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.CloseNow() }()
		b, _ := json.Marshal(map[string]any{"t": "hello", "v": agent.ProtocolVersion, "kind": "node",
			"node_id": uuid.NewString(), "worker_id": "w", "pid": 1, "host": "h",
			"eph":   base64.StdEncoding.EncodeToString(make([]byte, 32)),
			"nonce": base64.StdEncoding.EncodeToString(make([]byte, 16))})
		if err := c.Write(ctx, websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
		_, _, err = c.Read(ctx)
		var ce websocket.CloseError
		if !errors.As(err, &ce) {
			t.Fatalf("answer = %v", err)
		}
		return ce.Code
	}
	// two machines behind the same proxy: separate buckets
	for i := 0; i < 2; i++ {
		if code := hello("10.0.0.7"); code != agent.CloseNotMember {
			t.Fatalf("attempt %d from 10.0.0.7: %d", i, code)
		}
	}
	if code := hello("10.0.0.7"); code != agent.CloseRateLimited {
		t.Fatalf("third from 10.0.0.7: %d", code)
	}
	if code := hello("10.0.0.8"); code != agent.CloseNotMember {
		t.Fatalf("10.0.0.8 limited with 10.0.0.7: %d", code)
	}
	// and the refusals are filed with the client's address
	events := filepath.Join(dir, "192.168.1.2", "events", time.Now().UTC().Format("2006-01-02"), "hub.jsonl")
	deadline := time.Now().Add(4 * time.Second)
	for {
		ips := map[string]bool{}
		if f, err := os.Open(events); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				var m map[string]any
				if json.Unmarshal(sc.Bytes(), &m) == nil && m["event"] == "auth_refused" {
					ip, _ := m["ip"].(string)
					ips[ip] = true
				}
			}
			_ = f.Close()
		}
		if ips["10.0.0.7"] && ips["10.0.0.8"] {
			if ips["127.0.0.1"] {
				t.Fatal("a refusal was filed under the proxy's address")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("refusals filed under %v", ips)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
